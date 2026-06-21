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
// ClaimNextForWorker(). Use after `WHERE status = ?` (todo). Two additional
// `?` placeholders follow, in order: prerequisite-status (done) and
// active-claim status (doing). Keep parameter order in sync with both callers.
const readyWhereSQL = `
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
	db  *sql.DB
	now func() time.Time
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
	priority, tags, cwd, source, agent, group_id, resource_key, stage
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
	relType, err := validateRelation(t.ID, dependsOnID, task.RelationBlocks)
	if err != nil {
		return err
	}
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
	if _, err := getTask(ctx, tx, dependsOnID); err != nil {
		return err
	}
	cycle, err := dependencyPathExists(ctx, tx, dependsOnID, t.ID)
	if err != nil {
		return err
	}
	if cycle {
		return ErrDependencyCycle
	}
	created := s.nowString()
	if err := s.insertRelation(ctx, tx, t.ID, dependsOnID, relType, created); err != nil {
		return err
	}
	if err := s.recordRelationEvent(ctx, tx, t.ID, dependsOnID, relType, "", created); err != nil {
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
	priority, tags, cwd, source, agent, group_id, resource_key, stage
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Created, string(t.Status), t.Body, t.Started, t.Finished, t.Error, ordinal,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage); err != nil {
		if isDuplicateTaskID(err) {
			return fmt.Errorf("store: add task %s: %w", t.ID, ErrDuplicateTask)
		}
		return fmt.Errorf("store: add task %s: %w", t.ID, err)
	}
	return nil
}

// Update mutates one task. If fn returns false, no write occurs.
func (s *SQLiteStore) Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error {
	return s.updateImpl(ctx, id, "", event, message, fn)
}

func (s *SQLiteStore) updateImpl(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin update: %w", err)
	}
	defer rollback(tx)

	t, err := getTask(ctx, tx, id)
	if err != nil {
		return err
	}
	if expectWorker != "" {
		if t.Status != task.StatusDoing {
			return ErrWorkerMismatch
		}
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT worker_id FROM task_attempts WHERE task_id = ? AND finished = '' ORDER BY id DESC LIMIT 1`, id).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: fence owner lookup %s: %w", id, err)
		}
		if owner != expectWorker {
			return ErrWorkerMismatch
		}
	}
	if !fn(&t) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE tasks
SET created = ?, status = ?, body = ?, started = ?, lease_expires = ?, finished = ?, error = ?,
	priority = ?, tags = ?, cwd = ?, source = ?, agent = ?, group_id = ?, resource_key = ?, stage = ?
WHERE id = ?`,
		t.Created, string(t.Status), t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage, t.ID); err != nil {
		return fmt.Errorf("store: update task %s: %w", id, err)
	}
	at := s.eventTime(t)
	if err := s.insertEvent(ctx, tx, id, event, at, message); err != nil {
		return err
	}
	if err := updateAttemptForEvent(ctx, tx, t, event, at, message); err != nil {
		return err
	}
	return commit(tx)
}

func (s *SQLiteStore) UpdateFenced(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) error {
	return s.updateImpl(ctx, id, expectWorker, event, message, fn)
}

// Delete removes the task with id.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin delete: %w", err)
	}
	defer rollback(tx)

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
