package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const sqliteBusyRetryDelay = 25 * time.Millisecond

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
	return retrySQLiteBusy(ctx, s.migrateTaskMetadata)
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

func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "database is locked")
}
