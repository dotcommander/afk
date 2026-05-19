package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/queue"
	"github.com/dotcommander/afk/internal/task"
	_ "modernc.org/sqlite"
)

const importedJSONLKey = "imported_jsonl"

// SQLiteStore persists tasks in SQLite. JSONL is used only as an import source.
type SQLiteStore struct {
	db *sql.DB
}

// Paths identifies the SQLite DB path and legacy JSONL import path.
type Paths struct {
	SQLitePath string
	JSONLPath  string
}

// ResolvePaths derives SQLite and legacy JSONL paths from a user-provided path.
func ResolvePaths(path string) Paths {
	if path == "" {
		jsonl, _ := queue.DefaultPath()
		return Paths{
			SQLitePath: replaceExt(jsonl, ".sqlite"),
			JSONLPath:  jsonl,
		}
	}
	if strings.HasSuffix(path, ".jsonl") {
		return Paths{
			SQLitePath: replaceExt(path, ".sqlite"),
			JSONLPath:  path,
		}
	}
	return Paths{
		SQLitePath: path,
		JSONLPath:  replaceExt(path, ".jsonl"),
	}
}

// DefaultPath returns the default SQLite queue path.
func DefaultPath() (string, error) {
	jsonl, err := queue.DefaultPath()
	if err != nil {
		return "", err
	}
	return replaceExt(jsonl, ".sqlite"), nil
}

// NewSQLite opens a SQLite-backed store and imports legacy JSONL if needed.
func NewSQLite(ctx context.Context, paths Paths) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(paths.SQLitePath), 0o750); err != nil {
		return nil, fmt.Errorf("store: mkdir sqlite dir: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(paths.SQLitePath))
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite %s: %w", paths.SQLitePath, err)
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.init(ctx, paths.JSONLPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the SQLite handle.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// List returns all tasks in insertion order.
func (s *SQLiteStore) List(ctx context.Context) ([]task.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key
FROM tasks
ORDER BY ordinal, rowid`)
	if err != nil {
		return nil, fmt.Errorf("store: list: %w", err)
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
		return nil, fmt.Errorf("store: list rows: %w", err)
	}
	return tasks, nil
}

// Ready returns tasks that are currently eligible to be claimed, in scheduler order.
func (s *SQLiteStore) Ready(ctx context.Context, opts ReadyOptions) ([]task.Task, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key
FROM tasks
WHERE status = ?
AND NOT EXISTS (
	SELECT 1
	FROM task_dependencies d
	JOIN tasks prereq ON prereq.id = d.depends_on_id
	WHERE d.task_id = tasks.id
	AND prereq.status != ?
)
AND NOT EXISTS (
	SELECT 1
	FROM task_blocks b
	WHERE b.task_id = tasks.id
)
AND (
	resource_key = ''
	OR NOT EXISTS (
		SELECT 1
		FROM tasks active
		WHERE active.status = ?
		AND active.resource_key = tasks.resource_key
		AND active.id != tasks.id
		AND (
			active.lease_expires = ''
			OR active.lease_expires > ?
		)
	)
)
ORDER BY ordinal, rowid`, string(task.StatusPending), string(task.StatusDone), string(task.StatusWorking), now.UTC().Format(time.RFC3339))
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

// Events returns durable lifecycle events for a task.
func (s *SQLiteStore) Events(ctx context.Context, taskID string) ([]task.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, type, at, message
FROM task_events
WHERE task_id = ?
ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: events: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var events []task.Event
	for rows.Next() {
		var event task.Event
		var typ string
		if err := rows.Scan(&event.ID, &event.TaskID, &typ, &event.At, &event.Message); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		event.Type = task.EventType(typ)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: event rows: %w", err)
	}
	return events, nil
}

// Attempts returns execution attempts for a task.
func (s *SQLiteStore) Attempts(ctx context.Context, taskID string) ([]task.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, started, finished, status, error, worker_id, agent
FROM task_attempts
WHERE task_id = ?
ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: attempts: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var attempts []task.Attempt
	for rows.Next() {
		var attempt task.Attempt
		var status string
		if err := rows.Scan(&attempt.ID, &attempt.TaskID, &attempt.Started, &attempt.Finished, &status, &attempt.Error, &attempt.WorkerID, &attempt.Agent); err != nil {
			return nil, fmt.Errorf("store: scan attempt: %w", err)
		}
		attempt.Status = task.Status(status)
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: attempt rows: %w", err)
	}
	return attempts, nil
}

// RequeueStale resets working tasks whose lease expired or whose start time is older than olderThan.
func (s *SQLiteStore) RequeueStale(ctx context.Context, olderThan time.Duration, now time.Time) ([]task.Task, error) {
	cutoff := now.Add(-olderThan).UTC().Format(time.RFC3339)
	nowText := now.UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key
FROM tasks
WHERE status = ?
AND (
	(lease_expires != '' AND lease_expires <= ?)
	OR (lease_expires = '' AND started != '' AND started <= ?)
)
ORDER BY ordinal, rowid`, string(task.StatusWorking), nowText, cutoff)
	if err != nil {
		return nil, fmt.Errorf("store: list stale: %w", err)
	}
	var stale []task.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		stale = append(stale, t)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close stale rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: stale rows: %w", err)
	}
	for _, t := range stale {
		id := t.ID
		if err := s.Update(ctx, id, task.EventRequeued, "stale", func(tk *task.Task) bool {
			tk.Reset()
			return true
		}); err != nil {
			return nil, err
		}
	}
	return stale, nil
}

// Add appends t to the queue.
func (s *SQLiteStore) Add(ctx context.Context, t task.Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin add: %w", err)
	}
	defer rollback(tx)

	ordinal, err := nextOrdinal(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO tasks (
	id, created, status, body, started, finished, error, ordinal,
	priority, tags, cwd, source, agent, group_id, resource_key
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Created, string(t.Status), t.Body, t.Started, t.Finished, t.Error, ordinal,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey); err != nil {
		if isDuplicateTaskID(err) {
			return fmt.Errorf("store: add task %s: %w", t.ID, ErrDuplicateTask)
		}
		return fmt.Errorf("store: add task %s: %w", t.ID, err)
	}
	if err := insertEvent(ctx, tx, t.ID, task.EventAdded, t.Created, ""); err != nil {
		return err
	}
	return commit(tx)
}

// BulkAdd inserts tasks and dependency edges in a single transaction.
// Tasks are inserted with sequential ordinals; each dependency edge is
// cycle-checked against the in-progress graph before insertion.
func (s *SQLiteStore) BulkAdd(ctx context.Context, tasks []task.Task, deps []task.Dependency) error {
	if len(tasks) == 0 && len(deps) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin bulk add: %w", err)
	}
	defer rollback(tx)

	if err := bulkInsertTasks(ctx, tx, tasks); err != nil {
		return err
	}
	if err := bulkInsertDeps(ctx, tx, deps); err != nil {
		return err
	}
	return commit(tx)
}

// bulkInsertTasks inserts each task with a fresh ordinal and emits an EventAdded.
// Caller owns the transaction; on error the caller's deferred rollback fires.
func bulkInsertTasks(ctx context.Context, tx *sql.Tx, tasks []task.Task) error {
	for _, t := range tasks {
		ordinal, err := nextOrdinal(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO tasks (
	id, created, status, body, started, finished, error, ordinal,
	priority, tags, cwd, source, agent, group_id, resource_key
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Created, string(t.Status), t.Body, t.Started, t.Finished, t.Error, ordinal,
			t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey); err != nil {
			return fmt.Errorf("store: bulk add task %s: %w", t.ID, err)
		}
		if err := insertEvent(ctx, tx, t.ID, task.EventAdded, t.Created, ""); err != nil {
			return err
		}
	}
	return nil
}

// bulkInsertDeps validates and inserts each dependency edge, cycle-checking
// against the in-progress graph. Returns ErrInvalidDependency for malformed
// edges and ErrDependencyCycle if the new edge would close a cycle.
func bulkInsertDeps(ctx context.Context, tx *sql.Tx, deps []task.Dependency) error {
	for _, dep := range deps {
		if dep.TaskID == "" || dep.DependsOnID == "" || dep.TaskID == dep.DependsOnID {
			return ErrInvalidDependency
		}
		cycle, err := dependencyPathExists(ctx, tx, dep.DependsOnID, dep.TaskID)
		if err != nil {
			return err
		}
		if cycle {
			return ErrDependencyCycle
		}
		created := dep.Created
		if created == "" {
			created = nowString()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO task_dependencies (task_id, depends_on_id, created)
VALUES (?, ?, ?)
ON CONFLICT(task_id, depends_on_id) DO NOTHING`, dep.TaskID, dep.DependsOnID, created); err != nil {
			return fmt.Errorf("store: bulk add dependency %s -> %s: %w", dep.TaskID, dep.DependsOnID, err)
		}
		if err := insertEvent(ctx, tx, dep.TaskID, task.EventDependencyAdded, created, dep.DependsOnID); err != nil {
			return err
		}
	}
	return nil
}

// Update mutates one task. If fn returns false, no write occurs.
func (s *SQLiteStore) Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update: %w", err)
	}
	defer rollback(tx)

	t, err := getTask(ctx, tx, id)
	if err != nil {
		return err
	}
	if !fn(&t) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE tasks
SET created = ?, status = ?, body = ?, started = ?, lease_expires = ?, finished = ?, error = ?,
	priority = ?, tags = ?, cwd = ?, source = ?, agent = ?, group_id = ?, resource_key = ?
WHERE id = ?`,
		t.Created, string(t.Status), t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.ID); err != nil {
		return fmt.Errorf("store: update task %s: %w", id, err)
	}
	at := eventTime(t)
	if err := insertEvent(ctx, tx, id, event, at, message); err != nil {
		return err
	}
	if err := updateAttemptForEvent(ctx, tx, t, event, at, message); err != nil {
		return err
	}
	return commit(tx)
}

// Delete removes the task with id.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete task %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO task_events (task_id, type, at, message)
VALUES (?, ?, ?, ?)`, id, string(task.EventRemoved), nowString(), ""); err != nil {
		return fmt.Errorf("store: record remove event: %w", err)
	}
	return nil
}

// Prune removes all tasks with a listed status.
func (s *SQLiteStore) Prune(ctx context.Context, statuses []task.Status) error {
	for _, status := range statuses {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM tasks WHERE status = ?`, string(status))
		if err != nil {
			return fmt.Errorf("store: list prune %s: %w", status, err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return fmt.Errorf("store: scan prune id: %w", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("store: close prune ids: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("store: prune ids: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE status = ?`, string(status)); err != nil {
			return fmt.Errorf("store: prune %s: %w", status, err)
		}
		for _, id := range ids {
			if _, err := s.db.ExecContext(ctx, `
INSERT INTO task_events (task_id, type, at, message)
VALUES (?, ?, ?, ?)`, id, string(task.EventPruned), nowString(), string(status)); err != nil {
				return fmt.Errorf("store: record prune event: %w", err)
			}
		}
	}
	return nil
}

// PruneByTag deletes all tasks whose tags slice contains tag.
// Returns count deleted; returns an error if tag is empty.
func (s *SQLiteStore) PruneByTag(ctx context.Context, tag string) (int, error) {
	if tag == "" {
		return 0, fmt.Errorf("prune by tag: tag must not be empty")
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, tags FROM tasks`)
	if err != nil {
		return 0, fmt.Errorf("store: prune by tag scan: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id, rawTags string
		if err := rows.Scan(&id, &rawTags); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("store: prune by tag row: %w", err)
		}
		tags := decodeTags(rawTags)
		if slices.Contains(tags, tag) {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("store: prune by tag close rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: prune by tag rows: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin prune by tag: %w", err)
	}
	defer rollback(tx)

	now := nowString()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("store: prune by tag delete %s: %w", id, err)
		}
		if err := insertEvent(ctx, tx, id, task.EventPruned, now, "tag="+tag); err != nil {
			return 0, err
		}
	}
	if err := commit(tx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// ClaimNext atomically marks the first pending task working and returns it.
func (s *SQLiteStore) ClaimNext(ctx context.Context, now time.Time, leaseExpires time.Time) (*task.Task, error) {
	return s.ClaimNextForWorker(ctx, now, leaseExpires, "", "")
}

// ClaimNextForWorker atomically marks the first ready task working and records worker metadata.
func (s *SQLiteStore) ClaimNextForWorker(ctx context.Context, now time.Time, leaseExpires time.Time, workerID, agent string) (*task.Task, error) {
	started := now.UTC().Format(time.RFC3339)
	lease := ""
	if !leaseExpires.IsZero() {
		lease = leaseExpires.UTC().Format(time.RFC3339)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin claim: %w", err)
	}
	defer rollback(tx)

	row := tx.QueryRowContext(ctx, `
UPDATE tasks
SET status = ?, started = ?, lease_expires = ?
WHERE id = (
	SELECT id
	FROM tasks
	WHERE status = ?
	AND NOT EXISTS (
		SELECT 1
		FROM task_dependencies d
		JOIN tasks prereq ON prereq.id = d.depends_on_id
		WHERE d.task_id = tasks.id
		AND prereq.status != ?
	)
	AND NOT EXISTS (
		SELECT 1
		FROM task_blocks b
		WHERE b.task_id = tasks.id
	)
	AND (
		resource_key = ''
		OR NOT EXISTS (
			SELECT 1
			FROM tasks active
			WHERE active.status = ?
			AND active.resource_key = tasks.resource_key
			AND active.id != tasks.id
			AND (
				active.lease_expires = ''
				OR active.lease_expires > ?
			)
		)
	)
	ORDER BY ordinal, rowid
	LIMIT 1
)
RETURNING id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key`,
		string(task.StatusWorking), started, lease, string(task.StatusPending), string(task.StatusDone), string(task.StatusWorking), started)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := insertEvent(ctx, tx, t.ID, task.EventClaimed, started, ""); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_attempts (task_id, started, status, error, worker_id, agent)
VALUES (?, ?, ?, ?, ?, ?)`, t.ID, started, string(task.StatusWorking), "", workerID, agent); err != nil {
		return nil, fmt.Errorf("store: insert attempt: %w", err)
	}
	if err := commit(tx); err != nil {
		return nil, err
	}
	return &t, nil
}

// Heartbeat extends the lease for a worker-owned active attempt.
func (s *SQLiteStore) Heartbeat(ctx context.Context, taskID, workerID string, now time.Time, leaseExpires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin heartbeat: %w", err)
	}
	defer rollback(tx)

	t, err := getTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if t.Status != task.StatusWorking {
		return ErrInvalidState
	}
	var owner string
	err = tx.QueryRowContext(ctx, `
SELECT worker_id
FROM task_attempts
WHERE task_id = ? AND finished = ''
ORDER BY id DESC
LIMIT 1`, taskID).Scan(&owner)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidState
		}
		return fmt.Errorf("store: heartbeat owner %s: %w", taskID, err)
	}
	if owner != workerID {
		return ErrWorkerMismatch
	}
	lease := ""
	if !leaseExpires.IsZero() {
		lease = leaseExpires.UTC().Format(time.RFC3339)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE tasks
SET lease_expires = ?
WHERE id = ?`, lease, taskID); err != nil {
		return fmt.Errorf("store: heartbeat update %s: %w", taskID, err)
	}
	if err := insertEvent(ctx, tx, taskID, task.EventHeartbeat, now.UTC().Format(time.RFC3339), workerID); err != nil {
		return err
	}
	return commit(tx)
}

// AddDependency records that taskID is blocked by dependsOnID.
func (s *SQLiteStore) AddDependency(ctx context.Context, taskID, dependsOnID string) error {
	if taskID == "" || dependsOnID == "" || taskID == dependsOnID {
		return ErrInvalidDependency
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin add dependency: %w", err)
	}
	defer rollback(tx)

	if _, err := getTask(ctx, tx, taskID); err != nil {
		return err
	}
	if _, err := getTask(ctx, tx, dependsOnID); err != nil {
		return err
	}
	cycle, err := dependencyPathExists(ctx, tx, dependsOnID, taskID)
	if err != nil {
		return err
	}
	if cycle {
		return ErrDependencyCycle
	}

	created := nowString()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_dependencies (task_id, depends_on_id, created)
VALUES (?, ?, ?)
ON CONFLICT(task_id, depends_on_id) DO NOTHING`, taskID, dependsOnID, created); err != nil {
		return fmt.Errorf("store: add dependency %s -> %s: %w", taskID, dependsOnID, err)
	}
	if err := insertEvent(ctx, tx, taskID, task.EventDependencyAdded, created, dependsOnID); err != nil {
		return err
	}
	return commit(tx)
}

// RemoveDependency removes a blocked-by edge.
func (s *SQLiteStore) RemoveDependency(ctx context.Context, taskID, dependsOnID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin remove dependency: %w", err)
	}
	defer rollback(tx)

	res, err := tx.ExecContext(ctx, `
DELETE FROM task_dependencies
WHERE task_id = ? AND depends_on_id = ?`, taskID, dependsOnID)
	if err != nil {
		return fmt.Errorf("store: remove dependency %s -> %s: %w", taskID, dependsOnID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: remove dependency rows affected: %w", err)
	}
	if n == 0 {
		return ErrDependencyNotFound
	}
	if err := insertEvent(ctx, tx, taskID, task.EventDependencyRemoved, nowString(), dependsOnID); err != nil {
		return err
	}
	return commit(tx)
}

// Dependencies returns the tasks that taskID is blocked by.
func (s *SQLiteStore) Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, depends_on_id, created
FROM task_dependencies
WHERE task_id = ?
ORDER BY created, depends_on_id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: dependencies: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var deps []task.Dependency
	for rows.Next() {
		var dep task.Dependency
		if err := rows.Scan(&dep.TaskID, &dep.DependsOnID, &dep.Created); err != nil {
			return nil, fmt.Errorf("store: scan dependency: %w", err)
		}
		deps = append(deps, dep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: dependency rows: %w", err)
	}
	return deps, nil
}

// Block records or updates a manual scheduling block.
func (s *SQLiteStore) Block(ctx context.Context, taskID, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin block: %w", err)
	}
	defer rollback(tx)

	if _, err := getTask(ctx, tx, taskID); err != nil {
		return err
	}
	created := nowString()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_blocks (task_id, reason, created, created_by)
VALUES (?, ?, ?, '')
ON CONFLICT(task_id) DO UPDATE SET reason = excluded.reason, created = excluded.created`,
		taskID, reason, created); err != nil {
		return fmt.Errorf("store: block task %s: %w", taskID, err)
	}
	if err := insertEvent(ctx, tx, taskID, task.EventBlocked, created, reason); err != nil {
		return err
	}
	return commit(tx)
}

// Unblock removes a manual scheduling block.
func (s *SQLiteStore) Unblock(ctx context.Context, taskID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin unblock: %w", err)
	}
	defer rollback(tx)

	res, err := tx.ExecContext(ctx, `DELETE FROM task_blocks WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("store: unblock task %s: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: unblock rows affected: %w", err)
	}
	if n == 0 {
		return ErrBlockNotFound
	}
	if err := insertEvent(ctx, tx, taskID, task.EventUnblocked, nowString(), ""); err != nil {
		return err
	}
	return commit(tx)
}

// BlockForTask returns the manual scheduling block for taskID, if present.
func (s *SQLiteStore) BlockForTask(ctx context.Context, taskID string) (*task.Block, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT task_id, reason, created, created_by
FROM task_blocks
WHERE task_id = ?`, taskID)
	var block task.Block
	if err := row.Scan(&block.TaskID, &block.Reason, &block.Created, &block.CreatedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: block for task %s: %w", taskID, err)
	}
	return &block, nil
}

func sqliteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *SQLiteStore) init(ctx context.Context, jsonlPath string) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("store: enable wal: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("store: set busy timeout: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	created TEXT NOT NULL,
	status TEXT NOT NULL,
	body TEXT NOT NULL,
	started TEXT NOT NULL DEFAULT '',
	finished TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	lease_expires TEXT NOT NULL DEFAULT '',
	priority TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	cwd TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	agent TEXT NOT NULL DEFAULT '',
	group_id TEXT NOT NULL DEFAULT '',
	resource_key TEXT NOT NULL DEFAULT '',
	ordinal INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_status_order_idx ON tasks(status, ordinal);
CREATE TABLE IF NOT EXISTS metadata (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS task_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	type TEXT NOT NULL,
	at TEXT NOT NULL,
	message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS task_events_task_idx ON task_events(task_id, id);
CREATE TABLE IF NOT EXISTS task_attempts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	started TEXT NOT NULL,
	finished TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	worker_id TEXT NOT NULL DEFAULT '',
	agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS task_attempts_task_idx ON task_attempts(task_id, id);
CREATE TABLE IF NOT EXISTS task_dependencies (
	task_id TEXT NOT NULL,
	depends_on_id TEXT NOT NULL,
	created TEXT NOT NULL,
	PRIMARY KEY (task_id, depends_on_id)
);
CREATE INDEX IF NOT EXISTS task_dependencies_depends_on_idx ON task_dependencies(depends_on_id);
CREATE TABLE IF NOT EXISTS task_blocks (
	task_id TEXT PRIMARY KEY,
	reason TEXT NOT NULL,
	created TEXT NOT NULL,
	created_by TEXT NOT NULL DEFAULT ''
);
`); err != nil {
		return fmt.Errorf("store: create schema: %w", err)
	}
	if err := s.migrateTaskMetadata(ctx); err != nil {
		return err
	}
	return s.importJSONL(ctx, jsonlPath)
}

func (s *SQLiteStore) importJSONL(ctx context.Context, jsonlPath string) error {
	if jsonlPath == "" {
		return nil
	}
	if _, err := os.Stat(jsonlPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("store: stat jsonl import path: %w", err)
	}
	var imported string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = ?`, importedJSONLKey).Scan(&imported)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: read import metadata: %w", err)
	}
	if imported == jsonlPath {
		return nil
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM tasks`).Scan(&count); err != nil {
		return fmt.Errorf("store: count tasks: %w", err)
	}
	if count > 0 {
		return markImported(ctx, s.db, jsonlPath)
	}

	tasks, err := queue.New(jsonlPath).Load(ctx)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return markImported(ctx, s.db, jsonlPath)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin import: %w", err)
	}
	defer rollback(tx)
	for i, t := range tasks {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO tasks (
	id, created, status, body, started, finished, error, ordinal,
	priority, tags, cwd, source, agent, group_id, resource_key
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Created, string(t.Status), t.Body, t.Started, t.Finished, t.Error, i+1,
			t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey); err != nil {
			return fmt.Errorf("store: import task %s: %w", t.ID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO metadata (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, importedJSONLKey, jsonlPath); err != nil {
		return fmt.Errorf("store: write import metadata: %w", err)
	}
	return commit(tx)
}

func (s *SQLiteStore) migrateTaskMetadata(ctx context.Context) error {
	columns := []struct {
		name string
		sql  string
	}{
		{"priority", `ALTER TABLE tasks ADD COLUMN priority TEXT NOT NULL DEFAULT ''`},
		{"tags", `ALTER TABLE tasks ADD COLUMN tags TEXT NOT NULL DEFAULT '[]'`},
		{"cwd", `ALTER TABLE tasks ADD COLUMN cwd TEXT NOT NULL DEFAULT ''`},
		{"source", `ALTER TABLE tasks ADD COLUMN source TEXT NOT NULL DEFAULT ''`},
		{"agent", `ALTER TABLE tasks ADD COLUMN agent TEXT NOT NULL DEFAULT ''`},
		{"group_id", `ALTER TABLE tasks ADD COLUMN group_id TEXT NOT NULL DEFAULT ''`},
		{"resource_key", `ALTER TABLE tasks ADD COLUMN resource_key TEXT NOT NULL DEFAULT ''`},
		{"lease_expires", `ALTER TABLE tasks ADD COLUMN lease_expires TEXT NOT NULL DEFAULT ''`},
		{"task_attempts.worker_id", `ALTER TABLE task_attempts ADD COLUMN worker_id TEXT NOT NULL DEFAULT ''`},
		{"task_attempts.agent", `ALTER TABLE task_attempts ADD COLUMN agent TEXT NOT NULL DEFAULT ''`},
	}
	for _, col := range columns {
		if _, err := s.db.ExecContext(ctx, col.sql); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("store: migrate %s: %w", col.name, err)
		}
	}
	return nil
}

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(row taskScanner) (task.Task, error) {
	var t task.Task
	var status string
	var tags string
	if err := row.Scan(
		&t.ID, &t.Created, &status, &t.Body, &t.Started, &t.LeaseExpires, &t.Finished, &t.Error,
		&t.Priority, &tags, &t.CWD, &t.Source, &t.Agent, &t.GroupID, &t.ResourceKey,
	); err != nil {
		return task.Task{}, fmt.Errorf("store: scan task: %w", err)
	}
	t.Status = task.Status(status)
	t.Tags = decodeTags(tags)
	return t, nil
}

func getTask(ctx context.Context, tx *sql.Tx, id string) (task.Task, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key
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

func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

func isDuplicateTaskID(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: tasks.id")
}

func markImported(ctx context.Context, db *sql.DB, jsonlPath string) error {
	if _, err := db.ExecContext(ctx, `
INSERT INTO metadata (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, importedJSONLKey, jsonlPath); err != nil {
		return fmt.Errorf("store: write import metadata: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, taskID string, event task.EventType, at, message string) error {
	if at == "" {
		at = nowString()
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
	case task.EventDone, task.EventFailed:
		if _, err := tx.ExecContext(ctx, `
UPDATE task_attempts
SET finished = ?, status = ?, error = ?
WHERE id = (
	SELECT id FROM task_attempts
	WHERE task_id = ? AND finished = ''
	ORDER BY id DESC
	LIMIT 1
)`, at, string(t.Status), message, t.ID); err != nil {
			return fmt.Errorf("store: finish attempt %s: %w", t.ID, err)
		}
	case task.EventReset:
		if _, err := tx.ExecContext(ctx, `
UPDATE task_attempts
SET finished = ?, status = ?, error = ?
WHERE task_id = ? AND finished = ''`, at, string(task.StatusPending), "", t.ID); err != nil {
			return fmt.Errorf("store: reset attempt %s: %w", t.ID, err)
		}
	}
	return nil
}

func eventTime(t task.Task) string {
	if t.Finished != "" {
		return t.Finished
	}
	if t.Started != "" {
		return t.Started
	}
	return nowString()
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
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

func replaceExt(path, ext string) string {
	current := filepath.Ext(path)
	if current == "" {
		return path + ext
	}
	return strings.TrimSuffix(path, current) + ext
}
