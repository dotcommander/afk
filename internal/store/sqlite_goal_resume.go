package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// GoalResumeChanges contains only explicitly supplied cumulative-cap changes.
type GoalResumeChanges struct {
	MaxTokens     *int64
	MaxIterations *int64
	MaxDuration   *time.Duration
	TokenRegex    *string
}

// GoalResumeResult is the committed resume receipt.
type GoalResumeResult struct {
	Goal         task.GoalGroup `json:"budget"`
	ResumedTasks int            `json:"resumed_tasks"`
}

// ResumeGoal applies explicit cap changes and requeues all limited members in
// one transaction while preserving cumulative usage.
func (s *SQLiteStore) ResumeGoal(ctx context.Context, goalID string, changes GoalResumeChanges, now time.Time) (GoalResumeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalResumeResult{}, fmt.Errorf("store: begin resume goal: %w", err)
	}
	defer rollback(tx)
	goal, err := getGoalGroupTx(ctx, tx, goalID)
	if err != nil {
		return GoalResumeResult{}, err
	}
	applyGoalResumeChanges(&goal, changes)
	if err := validateGoalResumeUsage(goal); err != nil {
		return GoalResumeResult{}, err
	}
	ids, err := budgetLimitedTaskIDs(ctx, tx, goalID)
	if err != nil {
		return GoalResumeResult{}, err
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	if err := s.requeueBudgetLimitedTasks(ctx, tx, ids, nowText); err != nil {
		return GoalResumeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE goal_groups SET max_tokens=?,max_iterations=?,max_duration_ns=?,token_regex=?,budget_epoch_started='',limit_reason='',limited_at='' WHERE id=?`, goal.MaxTokens, goal.MaxIterations, int64(goal.MaxDuration), goal.TokenRegex, goalID); err != nil {
		return GoalResumeResult{}, fmt.Errorf("store: resume goal: %w", err)
	}
	if err := refreshGoalStatus(ctx, tx, goalID); err != nil {
		return GoalResumeResult{}, err
	}
	goal, err = getGoalGroupTx(ctx, tx, goalID)
	if err != nil {
		return GoalResumeResult{}, err
	}
	if err := commit(tx); err != nil {
		return GoalResumeResult{}, err
	}
	return GoalResumeResult{Goal: goal, ResumedTasks: len(ids)}, nil
}

func applyGoalResumeChanges(goal *task.GoalGroup, changes GoalResumeChanges) {
	if changes.MaxTokens != nil {
		goal.MaxTokens = *changes.MaxTokens
	}
	if changes.MaxIterations != nil {
		goal.MaxIterations = *changes.MaxIterations
	}
	if changes.MaxDuration != nil {
		goal.MaxDuration = *changes.MaxDuration
	}
	if changes.TokenRegex != nil {
		goal.TokenRegex = *changes.TokenRegex
	}
}

func validateGoalResumeUsage(goal task.GoalGroup) error {
	if goal.MaxTokens > 0 && goal.MaxTokens <= goal.TokensUsed {
		return fmt.Errorf("max tokens must exceed recorded usage %d", goal.TokensUsed)
	}
	if goal.MaxIterations > 0 && goal.MaxIterations <= goal.IterationsUsed {
		return fmt.Errorf("max iterations must exceed recorded usage %d", goal.IterationsUsed)
	}
	return nil
}

func budgetLimitedTaskIDs(ctx context.Context, tx *sql.Tx, goalID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE group_id=? AND status='budget-limited' ORDER BY ordinal`, goalID)
	if err != nil {
		return nil, fmt.Errorf("store: list resumable tasks: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err is authoritative
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan resumable task: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list resumable tasks: %w", err)
	}
	return ids, nil
}

func (s *SQLiteStore) requeueBudgetLimitedTasks(ctx context.Context, tx *sql.Tx, ids []string, nowText string) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status='todo',started='',lease_expires='',finished='',error='',revision=revision+1 WHERE id=? AND status='budget-limited'`, id); err != nil {
			return fmt.Errorf("store: requeue budget-limited task %s: %w", id, err)
		}
		if err := s.insertEvent(ctx, tx, id, task.EventRequeued, nowText, "goal budget resumed"); err != nil {
			return err
		}
	}
	return nil
}

func updateTaskRow(ctx context.Context, tx *sql.Tx, t task.Task) error {
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET created=?,status=?,body=?,started=?,lease_expires=?,finished=?,error=?,priority=?,tags=?,cwd=?,source=?,agent=?,group_id=?,resource_key=?,stage=?,available_at=?,revision=revision+1 WHERE id=?`, t.Created, t.Status, t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error, t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage, t.AvailableAt, t.ID)
	if err != nil {
		return fmt.Errorf("store: update goal task %s: %w", t.ID, err)
	}
	return nil
}

func getGoalGroupTx(ctx context.Context, tx *sql.Tx, id string) (task.GoalGroup, error) {
	var g task.GoalGroup
	err := tx.QueryRowContext(ctx, `SELECT id,objective,outcome,status,created,group_id,max_tokens,max_iterations,max_duration_ns,token_regex,budget_epoch_started,tokens_used,iterations_used,limit_reason,limited_at FROM goal_groups WHERE id=?`, id).Scan(&g.ID, &g.Objective, &g.Outcome, &g.Status, &g.CreatedAt, &g.GroupID, &g.MaxTokens, &g.MaxIterations, &g.MaxDuration, &g.TokenRegex, &g.BudgetEpochStarted, &g.TokensUsed, &g.IterationsUsed, &g.LimitReason, &g.LimitedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return task.GoalGroup{}, fmt.Errorf("goal group %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return task.GoalGroup{}, fmt.Errorf("store: get goal group %s: %w", id, err)
	}
	return g, nil
}

func mustParseRFC3339(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}
