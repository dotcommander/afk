package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(row taskScanner) (task.Task, error) {
	var t task.Task
	var status string
	var tags string
	if err := row.Scan(
		&t.ID, &t.Created, &status, &t.Body, &t.Started, &t.LeaseExpires, &t.Finished, &t.Error,
		&t.Priority, &tags, &t.CWD, &t.Source, &t.Agent, &t.GroupID, &t.ResourceKey, &t.Stage,
		&t.AvailableAt,
	); err != nil {
		return task.Task{}, fmt.Errorf("store: scan task: %w", err)
	}
	t.Status = task.NormalizeStatus(task.Status(status))
	t.Tags = decodeTags(tags)
	return t, nil
}

func getTask(ctx context.Context, tx *sql.Tx, id string) (task.Task, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage, available_at
FROM tasks
WHERE id = ?`, id)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return task.Task{}, fmt.Errorf("task %s: %w", id, ErrNotFound)
		}
		return task.Task{}, err
	}
	return t, nil
}

func dependencyPathExists(ctx context.Context, tx *sql.Tx, fromID, toID string) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `
WITH RECURSIVE dependency_path(id) AS (
	SELECT depends_on_id
	FROM task_dependencies
	WHERE task_id = ?
	UNION
	SELECT d.depends_on_id
	FROM task_dependencies d
	JOIN dependency_path p ON d.task_id = p.id
)
SELECT 1
FROM dependency_path
WHERE id = ?
LIMIT 1`, fromID, toID).Scan(&found)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("store: dependency path %s -> %s: %w", fromID, toID, err)
	}
	return true, nil
}

func nextOrdinal(ctx context.Context, tx *sql.Tx) (int, error) {
	var ordinal int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal), 0) + 1 FROM tasks`).Scan(&ordinal); err != nil {
		return 0, fmt.Errorf("store: next ordinal: %w", err)
	}
	return ordinal, nil
}

func encodeTags(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeTags(raw string) []string {
	if raw == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

// buildFTSMatch converts free-text query into an FTS5 MATCH expression. Each
// whitespace-delimited token is double-quoted (to neutralize FTS5 operators
// such as quotes, parens, AND/OR, and column filters in user input) and given
// a trailing prefix wildcard, then AND-joined. Returns "" for an all-whitespace
// query so callers can fall back to returning everything. Embedded double
// quotes inside a token are doubled per FTS5 string-literal escaping.
func buildFTSMatch(query string) string {
	var terms []string
	for _, tok := range strings.Fields(query) {
		escaped := strings.ReplaceAll(tok, `"`, `""`)
		terms = append(terms, `"`+escaped+`"*`)
	}
	return strings.Join(terms, " ")
}

func isDuplicateTaskID(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: tasks.id")
}

func (s *SQLiteStore) insertEvent(ctx context.Context, tx *sql.Tx, taskID string, event task.EventType, at, message string) error {
	if at == "" {
		at = s.nowString()
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_events (task_id, type, at, message)
VALUES (?, ?, ?, ?)`, taskID, string(event), at, message); err != nil {
		return fmt.Errorf("store: insert event %s for %s: %w", event, taskID, err)
	}
	return nil
}

func updateAttemptForEvent(ctx context.Context, tx *sql.Tx, t task.Task, event task.EventType, at, message string) error {
	switch event {
	case task.EventClaimed:
		if _, err := tx.ExecContext(ctx, `
INSERT INTO task_attempts (task_id, started, status, error, worker_id, agent)
VALUES (?, ?, ?, ?, ?, ?)`, t.ID, at, string(task.StatusDoing), "", "", ""); err != nil {
			return fmt.Errorf("store: insert attempt %s: %w", t.ID, err)
		}
	case task.EventDone, task.EventFailed:
		res, err := tx.ExecContext(ctx, `
UPDATE task_attempts
SET finished = ?, status = ?, error = ?
WHERE id = (
	SELECT id FROM task_attempts
	WHERE task_id = ? AND finished = ''
	ORDER BY id DESC
	LIMIT 1
)`, at, string(t.Status), message, t.ID)
		if err != nil {
			return fmt.Errorf("store: finish attempt %s: %w", t.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: finish attempt rows %s: %w", t.ID, err)
		}
		if n == 0 {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO task_attempts (task_id, started, finished, status, error, worker_id, agent)
VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, at, at, string(t.Status), message, "", ""); err != nil {
				return fmt.Errorf("store: synthesize attempt %s: %w", t.ID, err)
			}
		}
	case task.EventRequeued:
		if _, err := tx.ExecContext(ctx, `
UPDATE task_attempts SET finished = ?, status = ?, error = ?
WHERE id = (
	SELECT id FROM task_attempts
	WHERE task_id = ? AND finished = ''
	ORDER BY id DESC
	LIMIT 1
)`, at, string(t.Status), message, t.ID); err != nil {
			return fmt.Errorf("store: requeue attempt %s: %w", t.ID, err)
		}
	}
	return nil
}

func (s *SQLiteStore) eventTime(t task.Task) string {
	if t.Finished != "" {
		return t.Finished
	}
	if t.Started != "" {
		return t.Started
	}
	return s.nowString()
}

// nowString returns the current store clock formatted as RFC3339 UTC. The
// clock is settable via SetClock so tests can freeze event timestamps.
func (s *SQLiteStore) nowString() string {
	return s.now().UTC().Format(time.RFC3339)
}

// SetClock overrides the store's internal clock used for event and dependency
// timestamps generated inside the store (the prior code reached for
// time.Now() directly, which made those timestamps non-deterministic in
// tests). Passing nil resets to time.Now.
func (s *SQLiteStore) SetClock(now func() time.Time) {
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

func commit(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
