package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const sqliteBusyRetryDelay = 25 * time.Millisecond

// schemaVersionKey is the metadata row that records the highest migration
// version applied to this DB. Bump currentSchemaVersion whenever a new
// migration function is added below.
const (
	schemaVersionKey     = "schema_version"
	currentSchemaVersion = 4
)

func (s *SQLiteStore) init(ctx context.Context) error {
	if err := s.execWithBusyRetry(ctx, `
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
	stage TEXT NOT NULL DEFAULT '',
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
	relation_type TEXT NOT NULL DEFAULT 'blocks',
	PRIMARY KEY (task_id, depends_on_id)
);
CREATE INDEX IF NOT EXISTS task_dependencies_depends_on_idx ON task_dependencies(depends_on_id);
CREATE INDEX IF NOT EXISTS task_dependencies_type_idx ON task_dependencies(task_id, relation_type);
CREATE TABLE IF NOT EXISTS task_gates (
	task_id      TEXT NOT NULL,
	name         TEXT NOT NULL,
	satisfied    INTEGER NOT NULL DEFAULT 0,
	created      TEXT NOT NULL,
	satisfied_at TEXT,
	PRIMARY KEY (task_id, name)
);
CREATE INDEX IF NOT EXISTS task_gates_task_idx ON task_gates(task_id, satisfied);
`); err != nil {
		return fmt.Errorf("store: create schema: %w", err)
	}
	return retrySQLiteBusy(ctx, s.runMigrationsIfNeeded)
}

// runMigrationsIfNeeded reads the recorded schema_version and runs every
// historical migration exactly once, then records the new version. Skips all
// migration UPDATE/ALTER traffic on subsequent opens — important because
// these statements run on every NewSQLite call on the hot startup path.
func (s *SQLiteStore) runMigrationsIfNeeded(ctx context.Context) error {
	version, err := s.readSchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version >= currentSchemaVersion {
		return nil
	}
	if err := s.migrateTaskMetadata(ctx); err != nil {
		return err
	}
	if err := s.migrateStatusNames(ctx); err != nil {
		return err
	}
	return s.writeSchemaVersion(ctx, currentSchemaVersion)
}

// readSchemaVersion returns the recorded schema_version. A missing row
// returns 0, which is the "run every historical migration once" sentinel.
func (s *SQLiteStore) readSchemaVersion(ctx context.Context) (int, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = ?`, schemaVersionKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	version, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil {
		return 0, fmt.Errorf("store: parse schema version %q: %w", raw, convErr)
	}
	return version, nil
}

func (s *SQLiteStore) writeSchemaVersion(ctx context.Context, version int) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO metadata (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		schemaVersionKey, strconv.Itoa(version)); err != nil {
		return fmt.Errorf("store: write schema version: %w", err)
	}
	return nil
}

func (s *SQLiteStore) execWithBusyRetry(ctx context.Context, query string, args ...any) error {
	return retrySQLiteBusy(ctx, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, query, args...)
		return err
	})
}

func retrySQLiteBusy(ctx context.Context, fn func(context.Context) error) error {
	var err error
	for {
		err = fn(ctx)
		if !isSQLiteBusy(err) {
			return err
		}
		if waitErr := waitSQLiteBusyRetry(ctx); waitErr != nil {
			return err
		}
	}
}

func waitSQLiteBusyRetry(ctx context.Context) error {
	timer := time.NewTimer(sqliteBusyRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
		{"stage", `ALTER TABLE tasks ADD COLUMN stage TEXT NOT NULL DEFAULT ''`},
		{"lease_expires", `ALTER TABLE tasks ADD COLUMN lease_expires TEXT NOT NULL DEFAULT ''`},
		{"task_attempts.worker_id", `ALTER TABLE task_attempts ADD COLUMN worker_id TEXT NOT NULL DEFAULT ''`},
		{"task_attempts.agent", `ALTER TABLE task_attempts ADD COLUMN agent TEXT NOT NULL DEFAULT ''`},
		{"task_dependencies.relation_type", `ALTER TABLE task_dependencies ADD COLUMN relation_type TEXT NOT NULL DEFAULT 'blocks'`},
	}
	for _, col := range columns {
		if _, err := s.db.ExecContext(ctx, col.sql); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("store: migrate %s: %w", col.name, err)
		}
	}
	return nil
}

func (s *SQLiteStore) migrateStatusNames(ctx context.Context) error {
	updates := []struct {
		table string
		from  string
		to    string
	}{
		{table: "tasks", from: "pending", to: "todo"},
		{table: "tasks", from: "working", to: "doing"},
		{table: "task_attempts", from: "pending", to: "todo"},
		{table: "task_attempts", from: "working", to: "doing"},
	}
	for _, update := range updates {
		query := fmt.Sprintf("UPDATE %s SET status = ? WHERE status = ?", update.table)
		if _, err := s.db.ExecContext(ctx, query, update.to, update.from); err != nil {
			return fmt.Errorf("store: migrate %s status %s: %w", update.table, update.from, err)
		}
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

// isSQLiteBusy reports whether err is a SQLite contention error that the
// retry loop should back off on. Uses errors.As against *sqlite.Error so the
// check is robust to wrapped errors and message-string locale drift across
// driver versions.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}
