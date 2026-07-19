package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// Durable goal limit reasons persisted in goal_groups.limit_reason.
const (
	goalStatusBudgetLimited     = "budget-limited"
	LimitReasonTokens           = "max-tokens"
	LimitReasonIterations       = "max-iterations"
	LimitReasonDuration         = "max-duration"
	LimitReasonUsageUnavailable = "token-usage-unavailable" // #nosec G101 -- lifecycle reason, not a credential
)

// GoalFinalization is the complete outcome of one claimed goal invocation.
type GoalFinalization struct {
	TaskID          string
	AttemptID       int64
	WorkerID        string
	Succeeded       bool
	Error           string
	TokensUsed      int64
	TokensAvailable bool
	Now             time.Time
}

// GoalFinalizeResult describes the committed accounting and limit decision.
type GoalFinalizeResult struct {
	Goal    task.GoalGroup
	Limited bool
	Replay  bool
}

// PrepareGoalInvocation starts the current duration epoch and fail-closes an
// already-expired goal after claim but before spawning the agent.
func (s *SQLiteStore) PrepareGoalInvocation(ctx context.Context, taskID string, now time.Time) (task.GoalGroup, int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.GoalGroup{}, 0, false, fmt.Errorf("store: begin goal preflight: %w", err)
	}
	defer rollback(tx)
	member, err := getTask(ctx, tx, taskID)
	if err != nil {
		return task.GoalGroup{}, 0, false, err
	}
	var attemptID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM task_attempts WHERE task_id=? AND finished='' ORDER BY id DESC LIMIT 1`, taskID).Scan(&attemptID); err != nil {
		return task.GoalGroup{}, 0, false, fmt.Errorf("store: find active goal attempt: %w", err)
	}
	goal, err := getGoalGroupTx(ctx, tx, member.GroupID)
	if err != nil {
		return task.GoalGroup{}, 0, false, err
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	if goal.BudgetEpochStarted == "" {
		goal.BudgetEpochStarted = nowText
		if _, err := tx.ExecContext(ctx, `UPDATE goal_groups SET budget_epoch_started=? WHERE id=?`, nowText, goal.ID); err != nil {
			return task.GoalGroup{}, 0, false, fmt.Errorf("store: start goal budget epoch: %w", err)
		}
	}
	expired, err := goalDurationExceeded(goal, now)
	if err != nil {
		return task.GoalGroup{}, 0, false, err
	}
	if expired {
		if err := s.limitGoalTasks(ctx, tx, goal.ID, LimitReasonDuration, nowText); err != nil {
			return task.GoalGroup{}, 0, false, err
		}
		goal, err = getGoalGroupTx(ctx, tx, goal.ID)
		if err != nil {
			return task.GoalGroup{}, 0, false, err
		}
		if err := commit(tx); err != nil {
			return task.GoalGroup{}, 0, false, err
		}
		return goal, attemptID, true, nil
	}
	if err := commit(tx); err != nil {
		return task.GoalGroup{}, 0, false, err
	}
	return goal, attemptID, false, nil
}

func goalDurationExceeded(goal task.GoalGroup, now time.Time) (bool, error) {
	if goal.MaxDuration <= 0 || goal.BudgetEpochStarted == "" {
		return false, nil
	}
	started, err := time.Parse(time.RFC3339Nano, goal.BudgetEpochStarted)
	if err != nil {
		return false, fmt.Errorf("store: parse goal budget epoch: %w", err)
	}
	return now.Sub(started) >= goal.MaxDuration, nil
}

// FinalizeGoalInvocation atomically closes the task attempt, accounts the
// iteration once, and suspends remaining work when a durable cap is reached.
func (s *SQLiteStore) FinalizeGoalInvocation(ctx context.Context, in GoalFinalization) (GoalFinalizeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalFinalizeResult{}, fmt.Errorf("store: begin goal finalization: %w", err)
	}
	defer rollback(tx)
	member, err := getTask(ctx, tx, in.TaskID)
	if err != nil {
		return GoalFinalizeResult{}, err
	}
	goal, err := getGoalGroupTx(ctx, tx, member.GroupID)
	if err != nil {
		return GoalFinalizeResult{}, err
	}
	attemptID, accounted, err := goalAttemptAccounting(ctx, tx, goal.ID, in.TaskID, in.AttemptID)
	if err != nil {
		return GoalFinalizeResult{}, err
	}
	if accounted || member.Status == task.StatusBudgetLimited {
		return GoalFinalizeResult{Goal: goal, Limited: goal.Status == goalStatusBudgetLimited, Replay: true}, nil
	}
	if member.Status != task.StatusDoing {
		return GoalFinalizeResult{}, ErrInvalidState
	}
	var attemptWorker string
	if err := tx.QueryRowContext(ctx, `SELECT worker_id FROM task_attempts WHERE id=? AND task_id=?`, in.AttemptID, in.TaskID).Scan(&attemptWorker); err != nil {
		return GoalFinalizeResult{}, fmt.Errorf("store: find goal attempt owner: %w", err)
	}
	if in.WorkerID == "" || attemptWorker != in.WorkerID {
		return GoalFinalizeResult{}, ErrWorkerMismatch
	}
	if member.LeaseExpires != "" {
		deadline, parseErr := time.Parse(time.RFC3339, member.LeaseExpires)
		if parseErr != nil || !deadline.After(in.Now.UTC()) {
			return GoalFinalizeResult{}, ErrWorkerMismatch
		}
	}
	nowText := in.Now.UTC().Format(time.RFC3339Nano)
	if err := s.finishGoalMember(ctx, tx, &member, in, nowText); err != nil {
		return GoalFinalizeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO goal_iterations(goal_id,attempt_id,task_id,tokens_used,completed_at) VALUES (?,?,?,?,?)`, goal.ID, attemptID, member.ID, in.TokensUsed, nowText); err != nil {
		return GoalFinalizeResult{}, fmt.Errorf("store: account goal iteration: %w", err)
	}
	goal.TokensUsed += in.TokensUsed
	goal.IterationsUsed++
	reason, err := goalLimitReason(goal, in.TokensAvailable, in.Now)
	if err != nil {
		return GoalFinalizeResult{}, err
	}
	if err := s.persistGoalUsageAndLimit(ctx, tx, &goal, reason, nowText); err != nil {
		return GoalFinalizeResult{}, err
	}
	if err := commit(tx); err != nil {
		return GoalFinalizeResult{}, err
	}
	return GoalFinalizeResult{Goal: goal, Limited: reason != ""}, nil
}

func (s *SQLiteStore) persistGoalUsageAndLimit(ctx context.Context, tx *sql.Tx, goal *task.GoalGroup, reason, nowText string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE goal_groups SET tokens_used=?,iterations_used=? WHERE id=?`, goal.TokensUsed, goal.IterationsUsed, goal.ID); err != nil {
		return fmt.Errorf("store: update goal usage: %w", err)
	}
	if reason == "" {
		return refreshGoalStatus(ctx, tx, goal.ID)
	}
	if err := s.limitGoalTasks(ctx, tx, goal.ID, reason, nowText); err != nil {
		return err
	}
	refreshed, err := getGoalGroupTx(ctx, tx, goal.ID)
	if err != nil {
		return err
	}
	*goal = refreshed
	return nil
}

func goalAttemptAccounting(ctx context.Context, tx *sql.Tx, goalID, taskID string, attemptID int64) (int64, bool, error) {
	if attemptID <= 0 {
		return 0, false, errors.New("store: goal attempt id is required")
	}
	var finished string
	if err := tx.QueryRowContext(ctx, `SELECT finished FROM task_attempts WHERE id=? AND task_id=?`, attemptID, taskID).Scan(&finished); err != nil {
		return 0, false, fmt.Errorf("store: find goal attempt: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM goal_iterations WHERE goal_id=? AND attempt_id=?`, goalID, attemptID).Scan(&count); err != nil {
		return 0, false, fmt.Errorf("store: inspect goal iteration: %w", err)
	}
	if count != 0 || finished != "" {
		return attemptID, true, nil
	}
	var activeAttemptID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM task_attempts WHERE task_id=? AND finished='' ORDER BY id DESC LIMIT 1`, taskID).Scan(&activeAttemptID); err != nil {
		return 0, false, fmt.Errorf("store: find current goal attempt: %w", err)
	}
	return attemptID, activeAttemptID != attemptID, nil
}

func (s *SQLiteStore) finishGoalMember(ctx context.Context, tx *sql.Tx, member *task.Task, in GoalFinalization, nowText string) error {
	event := task.EventDone
	message := "completed by afk loop"
	if in.Succeeded {
		member.MarkDone(in.Now)
	} else {
		event, message = task.EventFailed, in.Error
		member.MarkFailed(in.Now, in.Error)
	}
	if err := updateTaskRow(ctx, tx, *member); err != nil {
		return err
	}
	if err := s.insertEvent(ctx, tx, member.ID, event, nowText, message); err != nil {
		return err
	}
	return updateAttemptForEvent(ctx, tx, *member, event, nowText, message)
}

func goalLimitReason(goal task.GoalGroup, tokensAvailable bool, now time.Time) (string, error) {
	switch {
	case goal.MaxTokens > 0 && !tokensAvailable:
		return LimitReasonUsageUnavailable, nil
	case goal.MaxTokens > 0 && goal.TokensUsed >= goal.MaxTokens:
		return LimitReasonTokens, nil
	case goal.MaxIterations > 0 && goal.IterationsUsed >= goal.MaxIterations:
		return LimitReasonIterations, nil
	}
	exceeded, err := goalDurationExceeded(goal, now)
	if err != nil {
		return "", err
	}
	if exceeded {
		return LimitReasonDuration, nil
	}
	return "", nil
}

func (s *SQLiteStore) limitGoalTasks(ctx context.Context, tx *sql.Tx, goalID, reason, nowText string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE group_id=? AND status IN ('todo','doing') ORDER BY ordinal`, goalID)
	if err != nil {
		return fmt.Errorf("store: list goal tasks to limit: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: scan goal task to limit: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("store: close goal limit rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: iterate goal tasks to limit: %w", err)
	}
	for _, id := range ids {
		member, err := getTask(ctx, tx, id)
		if err != nil {
			return err
		}
		member.MarkBudgetLimited(mustParseRFC3339(nowText), reason)
		if err := updateTaskRow(ctx, tx, member); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, id, task.EventBudgetLimited, nowText, reason); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE task_attempts SET finished=?,status=?,error=? WHERE task_id=? AND finished=''`, nowText, task.StatusBudgetLimited, reason, id); err != nil {
			return fmt.Errorf("store: close budget-limited attempt: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE goal_groups SET limit_reason=?,limited_at=? WHERE id=?`, reason, nowText, goalID); err != nil {
		return fmt.Errorf("store: limit goal: %w", err)
	}
	return refreshGoalStatus(ctx, tx, goalID)
}
