package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// AddGoalGroup inserts a goal_groups row.
func (s *SQLiteStore) AddGoalGroup(ctx context.Context, g task.GoalGroup) error {
	return s.execWithBusyRetry(ctx, `
INSERT INTO goal_groups (
 id,objective,outcome,status,created,group_id,max_tokens,max_iterations,max_duration_ns,token_regex,
 budget_epoch_started,tokens_used,iterations_used,limit_reason,limited_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.Objective, g.Outcome, g.Status, g.CreatedAt, g.GroupID,
		g.MaxTokens, g.MaxIterations, int64(g.MaxDuration), g.TokenRegex,
		g.BudgetEpochStarted, g.TokensUsed, g.IterationsUsed, g.LimitReason, g.LimitedAt)
}

// GetGoalGroup fetches a goal_groups row by id.
func (s *SQLiteStore) GetGoalGroup(ctx context.Context, id string) (task.GoalGroup, error) {
	var g task.GoalGroup
	err := retrySQLiteBusy(ctx, func(ctx context.Context) error {
		return s.db.QueryRowContext(ctx, `
SELECT id, objective, outcome, status, created, group_id,
       max_tokens, max_iterations, max_duration_ns, token_regex,
       budget_epoch_started, tokens_used, iterations_used, limit_reason, limited_at
FROM goal_groups
WHERE id = ?`, id).Scan(&g.ID, &g.Objective, &g.Outcome, &g.Status, &g.CreatedAt, &g.GroupID,
			&g.MaxTokens, &g.MaxIterations, &g.MaxDuration, &g.TokenRegex,
			&g.BudgetEpochStarted, &g.TokensUsed, &g.IterationsUsed, &g.LimitReason, &g.LimitedAt)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return task.GoalGroup{}, fmt.Errorf("goal group %s: %w", id, ErrNotFound)
		}
		return task.GoalGroup{}, fmt.Errorf("store: get goal group %s: %w", id, err)
	}
	return g, nil
}

// CreateGoal atomically inserts the durable group, member tasks, events, and
// ordered dependency chain.
func (s *SQLiteStore) CreateGoal(ctx context.Context, g task.GoalGroup, tasks []task.Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin create goal: %w", err)
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO goal_groups (
 id,objective,outcome,status,created,group_id,max_tokens,max_iterations,
 max_duration_ns,token_regex,budget_epoch_started,tokens_used,iterations_used,limit_reason,limited_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.Objective, g.Outcome, g.Status, g.CreatedAt, g.GroupID,
		g.MaxTokens, g.MaxIterations, int64(g.MaxDuration), g.TokenRegex,
		g.BudgetEpochStarted, g.TokensUsed, g.IterationsUsed, g.LimitReason, g.LimitedAt); err != nil {
		return fmt.Errorf("store: insert goal group: %w", err)
	}
	for i, member := range tasks {
		if err := s.insertTask(ctx, tx, member); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, member.ID, task.EventAdded, member.Created, ""); err != nil {
			return err
		}
		if i > 0 {
			if err := s.addDependencyInTx(ctx, tx, member.ID, tasks[i-1].ID); err != nil {
				return err
			}
		}
	}
	return commit(tx)
}

func refreshGoalStatus(ctx context.Context, tx *sql.Tx, groupID string) error {
	return refreshGoalStatusWithFallback(ctx, tx, groupID, "complete")
}

func refreshGoalStatusWithFallback(ctx context.Context, tx *sql.Tx, groupID, fallback string) error {
	if groupID == "" {
		return nil
	}
	var status string
	err := tx.QueryRowContext(ctx, `
SELECT CASE
 WHEN EXISTS(SELECT 1 FROM tasks WHERE group_id=? AND status IN ('todo','doing')) THEN 'active'
 WHEN EXISTS(SELECT 1 FROM tasks WHERE group_id=? AND status='budget-limited') THEN 'budget-limited'
 WHEN EXISTS(SELECT 1 FROM tasks WHERE group_id=? AND status IN ('failed','deleted')) THEN 'failed'
 ELSE ? END`, groupID, groupID, groupID, fallback).Scan(&status)
	if err != nil {
		return fmt.Errorf("store: reconcile goal status: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE goal_groups SET status=? WHERE id=? OR group_id=?`, status, groupID, groupID); err != nil {
		return fmt.Errorf("store: update goal status: %w", err)
	}
	return nil
}

// UpdateGoalGroupStatus changes a goal group's status.
func (s *SQLiteStore) UpdateGoalGroupStatus(ctx context.Context, id, status string) error {
	return s.execWithBusyRetry(ctx, `
UPDATE goal_groups SET status = ? WHERE id = ?`, status, id)
}

// CountTasksByGroupID returns per-status task counts for a single goal group,
// computed in SQL (GROUP BY) instead of scanning the whole task table.
func (s *SQLiteStore) CountTasksByGroupID(ctx context.Context, groupID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM tasks WHERE group_id = ? GROUP BY status`, groupID)
	if err != nil {
		return nil, fmt.Errorf("store: count tasks by group: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("store: count tasks by group: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: count tasks by group: %w", err)
	}
	return counts, nil
}

// BudgetLimitGroup suspends the still-active tasks in a group by setting their
// status to budget-limited. Only todo/doing tasks are affected; terminal tasks
// (done/failed/deleted) and already budget-limited tasks are left untouched.
func (s *SQLiteStore) BudgetLimitGroup(ctx context.Context, groupID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin budget-limit group: %w", err)
	}
	defer rollback(tx)
	if err := s.limitGoalTasks(ctx, tx, groupID, "budget-limited", s.nowString()); err != nil {
		return err
	}
	return commit(tx)
}
