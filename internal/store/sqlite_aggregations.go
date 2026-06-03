package store

import (
	"context"
	"fmt"
	"strings"

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

// statusStoredValues maps a canonical status to every raw stored value that
// normalizes to it — the canonical value plus any legacy alias. This mirrors
// the alias folding in task.NormalizeStatus so ListByStatus returns the same
// rows a NormalizeStatus-then-filter pass over List() would, including legacy
// "pending"/"working" rows written before the canonical names existed.
var statusStoredValues = map[task.Status][]string{
	task.StatusTodo:    {string(task.StatusTodo), "pending"},
	task.StatusDoing:   {string(task.StatusDoing), "working"},
	task.StatusDone:    {string(task.StatusDone)},
	task.StatusFailed:  {string(task.StatusFailed)},
	task.StatusDeleted: {string(task.StatusDeleted)},
}

// ListByStatus returns tasks whose stored status normalizes to the given
// canonical status, in List() order (ordinal, rowid). It scopes the read to
// the status index (tasks_status_order_idx) instead of scanning the whole
// table. status must be a canonical task.Status; callers normalize first.
func (s *SQLiteStore) ListByStatus(ctx context.Context, status task.Status) ([]task.Task, error) {
	stored, ok := statusStoredValues[task.NormalizeStatus(status)]
	if !ok {
		return nil, nil
	}
	placeholders := strings.Repeat("?, ", len(stored)-1) + "?"
	args := make([]any, len(stored))
	for i, v := range stored {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage
FROM tasks
WHERE status IN (`+placeholders+`)
ORDER BY ordinal, rowid`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list by status %s: %w", status, err)
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

func (s *SQLiteStore) listByStatuses(ctx context.Context, statuses ...string) ([]task.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage
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
