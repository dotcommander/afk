package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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
