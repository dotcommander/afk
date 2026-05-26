package store

import (
	"context"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// Counts returns per-status tallies computed in SQL. Keys are returned as
// stored (raw) — callers that need canonical keys must normalize. This
// preserves Service.Count's prior "raw bucket" behavior and lets a
// migration-detection layer notice legacy "pending"/"working" rows.
func (s *SQLiteStore) Counts(ctx context.Context) (map[task.Status]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("store: counts: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	tally := make(map[task.Status]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("store: counts scan: %w", err)
		}
		tally[task.Status(status)] += n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: counts rows: %w", err)
	}
	return tally, nil
}

// ActiveLists returns tasks in the todo and doing buckets via two indexed
// queries (one per canonical bucket). Both canonical and legacy status
// values are matched ("pending" → todo, "working" → doing) so the result
// matches a NormalizeStatus-then-bucket pass over a List() result.
func (s *SQLiteStore) ActiveLists(ctx context.Context) (todo, doing []task.Task, err error) {
	todo, err = s.listByStatuses(ctx, string(task.StatusTodo), "pending")
	if err != nil {
		return nil, nil, err
	}
	doing, err = s.listByStatuses(ctx, string(task.StatusDoing), "working")
	if err != nil {
		return nil, nil, err
	}
	return todo, doing, nil
}

func (s *SQLiteStore) listByStatuses(ctx context.Context, statuses ...string) ([]task.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key
FROM tasks
WHERE status IN (?, ?)
ORDER BY ordinal, rowid`, statuses[0], statuses[1])
	if err != nil {
		return nil, fmt.Errorf("store: list by status: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var tasks []task.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list by status rows: %w", err)
	}
	return tasks, nil
}
