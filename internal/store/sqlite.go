package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/task"
	_ "modernc.org/sqlite" // register the sqlite database/sql driver
)

const schedulerOrderSQL = `
CASE lower(trim(priority))
	WHEN 'urgent' THEN 0
	WHEN 'high' THEN 1
	WHEN 'low' THEN 3
	ELSE 2
END, ordinal, rowid`

// readyWhereSQL is the single readiness predicate shared by Ready() and
// ClaimNextForWorker(). Use after `WHERE status = ?` (todo). Three additional
// `?` placeholders follow, in order: current time, prerequisite-status (done),
// and active-claim status (doing). Keep parameter order in sync with callers.
const readyWhereSQL = `
AND (available_at = '' OR available_at <= ?)
AND NOT EXISTS (
	SELECT 1
	FROM task_dependencies d
	LEFT JOIN tasks prereq ON prereq.id = d.depends_on_id
	WHERE d.task_id = tasks.id
	AND d.relation_type = 'blocks'
	AND (prereq.id IS NULL OR prereq.status != ?)
)
AND (
	resource_key = ''
	OR NOT EXISTS (
		SELECT 1
		FROM tasks active
		WHERE active.status = ?
		AND active.resource_key = tasks.resource_key
		AND active.id != tasks.id
	)
)
AND NOT EXISTS (
	SELECT 1
	FROM task_gates g
	WHERE g.task_id = tasks.id
	AND g.satisfied = 0
)`

// SQLiteStore persists tasks in SQLite.
//
// now is the clock used for event/dependency timestamps generated inside the
// store. It is set to time.Now in NewSQLite and can be overridden by tests
// via SetClock so event ordering is deterministic. Centralizing the clock
// here keeps the test-injectable Service.now from escaping into wall-clock
// reads inside the store layer.
type SQLiteStore struct {
	db         *sql.DB
	now        func() time.Time
	schemaExec executor
}

// Paths identifies the SQLite DB path.
type Paths struct {
	SQLitePath string
}

// defaultRelPath is the SQLite queue path relative to the user's home dir.
const defaultRelPath = ".claude/queue/tasks.sqlite"

// ResolvePaths derives the SQLite path from a user-provided path. A path with
// a non-`.sqlite` extension (including a stale `.jsonl` path) is normalized to
// a sibling `.sqlite` database; any other path is used as-is.
func ResolvePaths(path string) Paths {
	if path == "" {
		sqlite, _ := DefaultPath()
		return Paths{SQLitePath: sqlite}
	}
	if ext := filepath.Ext(path); ext != "" && ext != ".sqlite" {
		return Paths{SQLitePath: strings.TrimSuffix(path, ext) + ".sqlite"}
	}
	return Paths{SQLitePath: path}
}

// DefaultPath returns the default SQLite queue path.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: resolve home dir: %w", err)
	}
	return filepath.Join(home, defaultRelPath), nil
}

// NewSQLite opens a SQLite-backed store.
func NewSQLite(ctx context.Context, paths Paths) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(paths.SQLitePath), 0o750); err != nil {
		return nil, fmt.Errorf("store: mkdir sqlite dir: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(paths.SQLitePath))
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite %s: %w", paths.SQLitePath, err)
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db, now: time.Now}
	if err := s.init(ctx); err != nil {
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
	priority, tags, cwd, source, agent, group_id, resource_key, stage, available_at
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

// Get returns a single task by id, using the primary-key index. Callers
// that need only one task should prefer Get over List+linear-scan.
func (s *SQLiteStore) Get(ctx context.Context, id string) (task.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, fmt.Errorf("store: begin get: %w", err)
	}
	defer rollback(tx)
	t, err := getTask(ctx, tx, id)
	if err != nil {
		return task.Task{}, err
	}
	if err := commit(tx); err != nil {
		return task.Task{}, err
	}
	return t, nil
}

// Add appends t to the queue.
func (s *SQLiteStore) Add(ctx context.Context, t task.Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin add: %w", err)
	}
	defer rollback(tx)

	if err := s.insertTask(ctx, tx, t); err != nil {
		return err
	}
	if err := s.insertEvent(ctx, tx, t.ID, task.EventAdded, t.Created, ""); err != nil {
		return err
	}
	return commit(tx)
}

// AddWithDependency inserts t and its blocking dependency in one transaction.
// If the dependency edge cannot be recorded, the task row is not left claimable.
func (s *SQLiteStore) AddWithDependency(ctx context.Context, t task.Task, dependsOnID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin add with dependency: %w", err)
	}
	defer rollback(tx)

	if err := s.insertTask(ctx, tx, t); err != nil {
		return err
	}
	if err := s.insertEvent(ctx, tx, t.ID, task.EventAdded, t.Created, ""); err != nil {
		return err
	}
	if err := s.addDependencyInTx(ctx, tx, t.ID, dependsOnID); err != nil {
		return err
	}
	return commit(tx)
}

func (s *SQLiteStore) insertTask(ctx context.Context, tx *sql.Tx, t task.Task) error {
	ordinal, err := nextOrdinal(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO tasks (
	id, created, status, body, started, finished, error, ordinal,
	priority, tags, cwd, source, agent, group_id, resource_key, available_at, stage
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Created, string(t.Status), t.Body, t.Started, t.Finished, t.Error, ordinal,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.AvailableAt, t.Stage); err != nil {
		if isDuplicateTaskID(err) {
			return fmt.Errorf("store: add task %s: %w", t.ID, ErrDuplicateTask)
		}
		return fmt.Errorf("store: add task %s: %w", t.ID, err)
	}
	return nil
}

// Update mutates one task. If fn returns false, no write occurs.
func (s *SQLiteStore) Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error {
	_, err := s.updateImpl(ctx, taskUpdate{
		id:      id,
		event:   event,
		message: message,
		mutate:  fn,
	})
	return err
}

type taskUpdate struct {
	id           string
	expectWorker string
	event        task.EventType
	message      string
	mutate       func(*task.Task) bool
}

func (s *SQLiteStore) updateImpl(ctx context.Context, update taskUpdate) (task.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, fmt.Errorf("store: begin update: %w", err)
	}
	defer rollback(tx)

	t, err := getTask(ctx, tx, update.id)
	if err != nil {
		return task.Task{}, err
	}
	if update.expectWorker != "" {
		if t.Status != task.StatusDoing {
			return task.Task{}, ErrWorkerMismatch
		}
		if t.LeaseExpires != "" {
			deadline, parseErr := time.Parse(time.RFC3339, t.LeaseExpires)
			if parseErr != nil || !deadline.After(s.now().UTC()) {
				return task.Task{}, ErrWorkerMismatch
			}
		}
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT worker_id FROM task_attempts WHERE task_id = ? AND finished = '' ORDER BY id DESC LIMIT 1`, update.id).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return task.Task{}, fmt.Errorf("store: fence owner lookup %s: %w", update.id, err)
		}
		if owner != update.expectWorker {
			return task.Task{}, ErrWorkerMismatch
		}
	}
	if !update.mutate(&t) {
		return t, nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE tasks
SET created = ?, status = ?, body = ?, started = ?, lease_expires = ?, finished = ?, error = ?,
	priority = ?, tags = ?, cwd = ?, source = ?, agent = ?, group_id = ?, resource_key = ?, stage = ?, available_at = ?, revision = revision + 1
WHERE id = ?`,
		t.Created, string(t.Status), t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage, t.AvailableAt, t.ID); err != nil {
		return task.Task{}, fmt.Errorf("store: update task %s: %w", update.id, err)
	}
	at := s.eventTime(t)
	if err := s.insertEvent(ctx, tx, update.id, update.event, at, update.message); err != nil {
		return task.Task{}, err
	}
	if err := updateAttemptForEvent(ctx, tx, t, update.event, at, update.message); err != nil {
		return task.Task{}, err
	}
	if err := refreshGoalStatus(ctx, tx, t.GroupID); err != nil {
		return task.Task{}, err
	}
	if err := commit(tx); err != nil {
		return task.Task{}, err
	}
	return t, nil
}

// UpdateFenced mutates a task only when expectWorker owns its active attempt.
func (s *SQLiteStore) UpdateFenced(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) error {
	_, err := s.UpdateFencedTask(ctx, id, expectWorker, event, message, fn)
	return err
}

// UpdateFencedTask mutates a worker-owned task and returns the committed snapshot.
func (s *SQLiteStore) UpdateFencedTask(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) (task.Task, error) {
	return s.updateImpl(ctx, taskUpdate{
		id:           id,
		expectWorker: expectWorker,
		event:        event,
		message:      message,
		mutate:       fn,
	})
}

// Delete removes the task with id.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin delete: %w", err)
	}
	defer rollback(tx)

	t, err := getTask(ctx, tx, id)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
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
	if err := s.insertEvent(ctx, tx, id, task.EventRemoved, "", ""); err != nil {
		return err
	}
	if t.GroupID != "" {
		if err := refreshGoalStatusWithFallback(ctx, tx, t.GroupID, "failed"); err != nil {
			return err
		}
	}
	return commit(tx)
}

func sqliteDSN(path string) string {
	// Normalize for SQLite URI form. On Windows, paths like C:\Users\foo must
	// become /C:/Users/foo so the leading drive letter is part of the path and
	// not parsed as a URI authority.
	p := filepath.ToSlash(path)
	if filepath.IsAbs(path) && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String()
}
