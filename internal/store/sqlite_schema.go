package store

import (
	"context"
	"fmt"
	"time"
)

const sqliteBusyRetryDelay = 25 * time.Millisecond

// schemaVersionKey is the metadata row that records the highest migration
// version applied to this DB. Bump currentSchemaVersion whenever a new
// migration function is added below.
const (
	schemaVersionKey     = "schema_version"
	currentSchemaVersion = 13
	schemaTableTasks     = "tasks"
	canonicalStatusTodo  = "todo"
	canonicalStatusDoing = "doing"
)

func (s *SQLiteStore) init(ctx context.Context) error {
	if err := retrySQLiteBusy(ctx, s.rejectNewerSchema); err != nil {
		return err
	}
	// Serialize schema setup across concurrent openers — in-process goroutines
	// and separate afk processes — with one IMMEDIATE transaction (_txlock).
	// Concurrent openers block on the write lock until the first commits; the
	// version short-circuit in runMigrationsIfNeeded then makes them run zero
	// migrations, so concurrent first-open cannot interleave DDL or backfill the
	// FTS index twice. busy_timeout(5000) + retrySQLiteBusy absorb lock waits.
	return retrySQLiteBusy(ctx, func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: begin schema tx: %w", err)
		}
		s.schemaExec = tx
		defer func() { s.schemaExec = nil }()
		defer func() { _ = tx.Rollback() }() // no-op after a successful Commit
		if err := s.createBaseSchema(ctx); err != nil {
			return err
		}
		if err := s.runMigrationsIfNeeded(ctx); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit schema tx: %w", err)
		}
		return nil
	})
}

// createBaseSchema runs the bootstrap CREATE TABLE/INDEX/TRIGGER DDL in a single
// busy-retried batch. Idempotent (IF NOT EXISTS); migrations layer on top.
//
//nolint:funlen // single cohesive bootstrap DDL string; splitting fragments one batch
func (s *SQLiteStore) createBaseSchema(ctx context.Context) error {
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
	available_at TEXT NOT NULL DEFAULT '',
	ordinal INTEGER NOT NULL,
	revision INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS tasks_status_order_idx ON tasks(status, ordinal);
CREATE INDEX IF NOT EXISTS tasks_group_id_status_idx ON tasks(group_id, status);
CREATE INDEX IF NOT EXISTS tasks_status_created_idx ON tasks(status, created);
CREATE INDEX IF NOT EXISTS tasks_status_started_idx ON tasks(status, started);
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
CREATE INDEX IF NOT EXISTS task_events_stale_requeue_at_idx ON task_events(at) WHERE type='requeued' AND message='stale';
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
CREATE INDEX IF NOT EXISTS task_attempts_started_idx ON task_attempts(started, task_id, id);
CREATE INDEX IF NOT EXISTS task_attempts_status_finished_idx ON task_attempts(status, finished);
CREATE TABLE IF NOT EXISTS task_dependencies (
	task_id TEXT NOT NULL,
	depends_on_id TEXT NOT NULL,
	created TEXT NOT NULL,
	relation_type TEXT NOT NULL DEFAULT 'blocks',
	PRIMARY KEY (task_id, depends_on_id)
);
CREATE INDEX IF NOT EXISTS task_dependencies_depends_on_idx ON task_dependencies(depends_on_id);
CREATE TABLE IF NOT EXISTS task_gates (
	task_id      TEXT NOT NULL,
	name         TEXT NOT NULL,
	satisfied    INTEGER NOT NULL DEFAULT 0,
	created      TEXT NOT NULL,
	satisfied_at TEXT,
	PRIMARY KEY (task_id, name)
);
CREATE INDEX IF NOT EXISTS task_gates_task_idx ON task_gates(task_id, satisfied);
CREATE TABLE IF NOT EXISTS request_ledger (
	actor TEXT NOT NULL,
	request_id TEXT NOT NULL,
	operation TEXT NOT NULL,
	state TEXT NOT NULL,
	result_json TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	completed_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (actor, request_id)
);
CREATE TABLE IF NOT EXISTS task_checkpoints (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	checkpoint_key TEXT NOT NULL DEFAULT '',
	value_json TEXT NOT NULL,
	source_system TEXT NOT NULL,
	source_id TEXT NOT NULL,
	source_event_id INTEGER,
	created_at TEXT NOT NULL,
	UNIQUE(task_id, source_system, source_id)
);
CREATE INDEX IF NOT EXISTS task_checkpoints_task_idx ON task_checkpoints(task_id, id);
CREATE TABLE IF NOT EXISTS task_artifacts (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	path TEXT NOT NULL,
	content_type TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	source_system TEXT NOT NULL,
	source_id TEXT NOT NULL,
	source_event_id INTEGER,
	created_at TEXT NOT NULL,
	UNIQUE(task_id, source_system, source_id)
);
CREATE INDEX IF NOT EXISTS task_artifacts_task_idx ON task_artifacts(task_id, created_at, id);
CREATE TABLE IF NOT EXISTS vybe_imports (
	source_sha256 TEXT PRIMARY KEY,
	cutover_id TEXT NOT NULL,
	report_json TEXT NOT NULL,
	imported_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS goal_groups (
	id TEXT PRIMARY KEY,
	objective TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	created TEXT NOT NULL DEFAULT '',
	group_id TEXT NOT NULL DEFAULT '',
	max_tokens INTEGER NOT NULL DEFAULT 0,
	max_iterations INTEGER NOT NULL DEFAULT 0,
	max_duration_ns INTEGER NOT NULL DEFAULT 0,
	token_regex TEXT NOT NULL DEFAULT '',
	budget_epoch_started TEXT NOT NULL DEFAULT '',
	tokens_used INTEGER NOT NULL DEFAULT 0,
	iterations_used INTEGER NOT NULL DEFAULT 0,
	limit_reason TEXT NOT NULL DEFAULT '',
	limited_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS goal_iterations (
	goal_id TEXT NOT NULL,
	attempt_id INTEGER NOT NULL,
	task_id TEXT NOT NULL,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	completed_at TEXT NOT NULL,
	PRIMARY KEY (goal_id, attempt_id)
);
CREATE INDEX IF NOT EXISTS goal_iterations_task_idx ON goal_iterations(task_id);
CREATE VIRTUAL TABLE IF NOT EXISTS tasks_fts USING fts5(
	id UNINDEXED,
	status,
	body,
	priority,
	cwd,
	source,
	agent,
	group_id,
	resource_key,
	error,
	tags,
	tokenize='unicode61'
);
CREATE TRIGGER IF NOT EXISTS tasks_fts_ai AFTER INSERT ON tasks BEGIN
	INSERT INTO tasks_fts(id, status, body, priority, cwd, source, agent, group_id, resource_key, error, tags)
	VALUES (new.id, new.status, new.body, new.priority, new.cwd, new.source, new.agent, new.group_id, new.resource_key, new.error, new.tags);
END;
CREATE TRIGGER IF NOT EXISTS tasks_fts_ad AFTER DELETE ON tasks BEGIN
	DELETE FROM tasks_fts WHERE id = old.id;
END;
CREATE TRIGGER IF NOT EXISTS tasks_fts_au AFTER UPDATE ON tasks
WHEN (old.status<>new.status OR old.body<>new.body OR old.priority<>new.priority OR old.cwd<>new.cwd OR old.source<>new.source OR old.agent<>new.agent OR old.group_id<>new.group_id OR old.resource_key<>new.resource_key OR old.error<>new.error OR old.tags<>new.tags)
BEGIN
	DELETE FROM tasks_fts WHERE id = old.id;
	INSERT INTO tasks_fts(id, status, body, priority, cwd, source, agent, group_id, resource_key, error, tags)
	VALUES (new.id, new.status, new.body, new.priority, new.cwd, new.source, new.agent, new.group_id, new.resource_key, new.error, new.tags);
END;
`); err != nil {
		return fmt.Errorf("store: create schema: %w", err)
	}
	return nil
}
