package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/dotcommander/afk/internal/task"
)

// Find returns tasks whose indexed metadata matches query, in insertion order,
// using the tasks_fts FTS5 index. An empty (whitespace-only) query returns all
// tasks (parity with List). The query is split on whitespace; each token is
// turned into a prefix term (term*) and AND-combined, so "fix sea" matches a
// task containing both a "fix*" and a "sea*" token. Special FTS5 syntax in the
// raw query is neutralized by quoting each token, so callers can pass arbitrary
// user text without triggering FTS5 parse errors.
func (s *SQLiteStore) Find(ctx context.Context, query string) ([]task.Task, error) {
	match := buildFTSMatch(query)
	if match == "" {
		return s.List(ctx)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, t.created, t.status, t.body, t.started, t.lease_expires, t.finished, t.error,
	t.priority, t.tags, t.cwd, t.source, t.agent, t.group_id, t.resource_key, t.stage, t.available_at
FROM tasks t
JOIN tasks_fts f ON f.id = t.id
WHERE tasks_fts MATCH ?
ORDER BY t.ordinal, t.rowid`, match)
	if err != nil {
		return nil, fmt.Errorf("store: find: %w", err)
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
		return nil, fmt.Errorf("store: find rows: %w", err)
	}
	return tasks, nil
}

// Ready returns tasks that are currently eligible to be claimed, in scheduler order.
func (s *SQLiteStore) Ready(ctx context.Context) ([]task.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage, available_at
FROM tasks
WHERE status = ?`+readyWhereSQL+`
ORDER BY `+schedulerOrderSQL, string(task.StatusTodo), s.nowString(), string(task.StatusDone), string(task.StatusDoing))
	if err != nil {
		return nil, fmt.Errorf("store: ready: %w", err)
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
		return nil, fmt.Errorf("store: ready rows: %w", err)
	}
	return tasks, nil
}

// RecentDistinctCWDs returns up to limit distinct non-empty task working
// directories. Directories are selected by their most-recently created task
// (GROUP BY cwd, ORDER BY MAX(created) DESC) so the freshest `limit` paths win,
// then the result is sorted alphabetically to match the ordering callers expect.
// A limit <= 0 means no limit.
func (s *SQLiteStore) RecentDistinctCWDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = -1 // SQLite: LIMIT -1 means no limit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT cwd
FROM tasks
WHERE cwd != ''
GROUP BY cwd
ORDER BY MAX(created) DESC, cwd
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent distinct cwds: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var paths []string
	for rows.Next() {
		var cwd string
		if err := rows.Scan(&cwd); err != nil {
			return nil, fmt.Errorf("store: scan cwd: %w", err)
		}
		paths = append(paths, cwd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: recent distinct cwd rows: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}
