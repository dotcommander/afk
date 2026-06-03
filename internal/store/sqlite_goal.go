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
INSERT INTO goal_groups (id, objective, status, created, group_id)
VALUES (?, ?, ?, ?, ?)`, g.ID, g.Objective, g.Status, g.CreatedAt, g.GroupID)
}

// GetGoalGroup fetches a goal_groups row by id.
func (s *SQLiteStore) GetGoalGroup(ctx context.Context, id string) (task.GoalGroup, error) {
	var g task.GoalGroup
	err := retrySQLiteBusy(ctx, func(ctx context.Context) error {
		return s.db.QueryRowContext(ctx, `
SELECT id, objective, status, created, group_id
FROM goal_groups
WHERE id = ?`, id).Scan(&g.ID, &g.Objective, &g.Status, &g.CreatedAt, &g.GroupID)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return task.GoalGroup{}, fmt.Errorf("goal group %s: %w", id, ErrNotFound)
		}
		return task.GoalGroup{}, fmt.Errorf("store: get goal group %s: %w", id, err)
	}
	return g, nil
}

// UpdateGoalGroupStatus changes a goal group's status.
func (s *SQLiteStore) UpdateGoalGroupStatus(ctx context.Context, id, status string) error {
	return s.execWithBusyRetry(ctx, `
UPDATE goal_groups SET status = ? WHERE id = ?`, status, id)
}

// BudgetLimitGroup suspends the still-active tasks in a group by setting their
// status to budget-limited. Only todo/doing tasks are affected; terminal tasks
// (done/failed/deleted) and already budget-limited tasks are left untouched.
func (s *SQLiteStore) BudgetLimitGroup(ctx context.Context, groupID string) error {
	return s.execWithBusyRetry(ctx, `
UPDATE tasks SET status = ?
WHERE group_id = ? AND status IN (?, ?)`,
		string(task.StatusBudgetLimited), groupID, string(task.StatusTodo), string(task.StatusDoing))
}
