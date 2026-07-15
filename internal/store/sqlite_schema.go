package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const sqliteBusyRetryDelay = 25 * time.Millisecond

// schemaVersionKey is the metadata row that records the highest migration
// version applied to this DB. Bump currentSchemaVersion whenever a new
// migration function is added below.
const (
	schemaVersionKey     = "schema_version"
	currentSchemaVersion = 11
	schemaTableTasks     = "tasks"
	canonicalStatusTodo  = "todo"
	canonicalStatusDoing = "doing"
)

func (s *SQLiteStore) init(ctx context.Context) error {
	if err := s.rejectNewerSchema(ctx); err != nil {
		return err
	}
	if err := s.createBaseSchema(ctx); err != nil {
		return err
	}
	return retrySQLiteBusy(ctx, s.runMigrationsIfNeeded)
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
	ordinal INTEGER NOT NULL,
	revision INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS tasks_status_order_idx ON tasks(status, ordinal);
CREATE INDEX IF NOT EXISTS tasks_group_id_status_idx ON tasks(group_id, status);
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

// runMigrationsIfNeeded reads the recorded schema_version and runs every
// historical migration exactly once, then records the new version. Skips all
// migration UPDATE/ALTER traffic on subsequent opens — important because
// these statements run on every NewSQLite call on the hot startup path.
func (s *SQLiteStore) runMigrationsIfNeeded(ctx context.Context) error {
	version, err := s.readSchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("store: schema version %d is newer than supported version %d", version, currentSchemaVersion)
	}
	if version == currentSchemaVersion {
		return nil
	}
	if err := s.migrateTaskMetadata(ctx); err != nil {
		return err
	}
	if err := s.migrateTaskDependencyTypeIndex(ctx); err != nil {
		return err
	}
	if err := s.migrateStatusNames(ctx); err != nil {
		return err
	}
	if err := s.backfillTasksFTS(ctx); err != nil {
		return err
	}
	if err := s.migrateV6BudgetLimited(ctx); err != nil {
		return err
	}
	if err := s.migrateV7GoalOutcome(ctx); err != nil {
		return err
	}
	if err := s.migrateV8FTSUpdateScope(ctx); err != nil {
		return err
	}
	if err := s.migrateV9RequestLedger(ctx); err != nil {
		return err
	}
	if err := s.migrateV10RetirementState(ctx); err != nil {
		return err
	}
	if err := s.migrateV11DurableGoals(ctx); err != nil {
		return err
	}
	return s.writeSchemaVersion(ctx, currentSchemaVersion)
}

// rejectNewerSchema runs before bootstrap DDL so opening a database produced by
// a newer AFK binary is fail-closed and side-effect free.
func (s *SQLiteStore) rejectNewerSchema(ctx context.Context) error {
	var metadataExists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='metadata'`).Scan(&metadataExists); err != nil {
		return fmt.Errorf("store: create schema preflight: %w", err)
	}
	if metadataExists == 0 {
		return nil
	}
	version, err := s.readSchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("store: schema version %d is newer than supported version %d", version, currentSchemaVersion)
	}
	return nil
}

func (s *SQLiteStore) migrateV11DurableGoals(ctx context.Context) error {
	columns := []string{
		`ALTER TABLE goal_groups ADD COLUMN max_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goal_groups ADD COLUMN max_iterations INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goal_groups ADD COLUMN max_duration_ns INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goal_groups ADD COLUMN token_regex TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE goal_groups ADD COLUMN budget_epoch_started TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE goal_groups ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goal_groups ADD COLUMN iterations_used INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goal_groups ADD COLUMN limit_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE goal_groups ADD COLUMN limited_at TEXT NOT NULL DEFAULT ''`,
	}
	for _, statement := range columns {
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("store: migrate durable goal column: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS goal_iterations (
	goal_id TEXT NOT NULL,
	attempt_id INTEGER NOT NULL,
	task_id TEXT NOT NULL,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	completed_at TEXT NOT NULL,
	PRIMARY KEY (goal_id, attempt_id)
);
CREATE INDEX IF NOT EXISTS goal_iterations_task_idx ON goal_iterations(task_id);
CREATE TRIGGER IF NOT EXISTS tasks_revision_au AFTER UPDATE ON tasks
WHEN new.revision = old.revision
BEGIN
	UPDATE tasks SET revision = old.revision + 1 WHERE id = old.id;
END;`); err != nil {
		return fmt.Errorf("store: migrate durable goals: %w", err)
	}
	return nil
}

func (s *SQLiteStore) migrateV10RetirementState(ctx context.Context) error {
	statements := []string{
		`ALTER TABLE tasks ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`,
		`CREATE TABLE IF NOT EXISTS task_checkpoints (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, kind TEXT NOT NULL, checkpoint_key TEXT NOT NULL DEFAULT '', value_json TEXT NOT NULL, source_system TEXT NOT NULL, source_id TEXT NOT NULL, source_event_id INTEGER, created_at TEXT NOT NULL, UNIQUE(task_id, source_system, source_id))`,
		`CREATE INDEX IF NOT EXISTS task_checkpoints_task_idx ON task_checkpoints(task_id, id)`,
		`CREATE TABLE IF NOT EXISTS task_artifacts (id TEXT PRIMARY KEY, task_id TEXT NOT NULL, path TEXT NOT NULL, content_type TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', source_system TEXT NOT NULL, source_id TEXT NOT NULL, source_event_id INTEGER, created_at TEXT NOT NULL, UNIQUE(task_id, source_system, source_id))`,
		`CREATE INDEX IF NOT EXISTS task_artifacts_task_idx ON task_artifacts(task_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS vybe_imports (source_sha256 TEXT PRIMARY KEY, cutover_id TEXT NOT NULL, report_json TEXT NOT NULL, imported_at TEXT NOT NULL)`,
	}
	for i, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil && (i != 0 || !strings.Contains(err.Error(), "duplicate column name")) {
			return fmt.Errorf("store: migrate retirement state: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) migrateV9RequestLedger(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS request_ledger (
	actor TEXT NOT NULL,
	request_id TEXT NOT NULL,
	operation TEXT NOT NULL,
	state TEXT NOT NULL,
	result_json TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	completed_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (actor, request_id)
)`); err != nil {
		return fmt.Errorf("store: migrate request ledger: %w", err)
	}
	return nil
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

// migrateTaskDependencyTypeIndex creates the task_dependencies(task_id,
// relation_type) index. It runs in the migration phase rather than the base
// DDL because the index references the relation_type column, which is added by
// migrateTaskMetadata on legacy DBs that predate it. Creating the index here —
// after that ALTER — avoids a "no such column: relation_type" failure on open.
// CREATE INDEX IF NOT EXISTS keeps it idempotent for fresh and re-run DBs.
func (s *SQLiteStore) migrateTaskDependencyTypeIndex(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS task_dependencies_type_idx ON task_dependencies(task_id, relation_type)`); err != nil {
		return fmt.Errorf("store: migrate task_dependencies_type_idx: %w", err)
	}
	return nil
}

// migrateV6BudgetLimited applies schema version 6: the StatusBudgetLimited task
// status (the status column is already TEXT, so no column change is needed) and
// the goal_groups table. CREATE TABLE IF NOT EXISTS keeps it idempotent so
// fresh-DB init and an upgrade from v5 both converge to the same schema.
func (s *SQLiteStore) migrateV6BudgetLimited(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS goal_groups (
	id TEXT PRIMARY KEY,
	objective TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	created TEXT NOT NULL DEFAULT '',
	group_id TEXT NOT NULL DEFAULT ''
)`); err != nil {
		return fmt.Errorf("store: migrate goal_groups: %w", err)
	}
	return nil
}

func (s *SQLiteStore) migrateStatusNames(ctx context.Context) error {
	updates := []struct {
		table string
		from  string
		to    string
		query string
	}{
		{table: schemaTableTasks, from: legacyStatusPending, to: canonicalStatusTodo, query: `UPDATE tasks SET status = ? WHERE status = ?`},
		{table: schemaTableTasks, from: legacyStatusWorking, to: canonicalStatusDoing, query: `UPDATE tasks SET status = ? WHERE status = ?`},
		{table: "task_attempts", from: legacyStatusPending, to: canonicalStatusTodo, query: `UPDATE task_attempts SET status = ? WHERE status = ?`},
		{table: "task_attempts", from: legacyStatusWorking, to: canonicalStatusDoing, query: `UPDATE task_attempts SET status = ? WHERE status = ?`},
	}
	for _, update := range updates {
		if _, err := s.db.ExecContext(ctx, update.query, update.to, update.from); err != nil {
			return fmt.Errorf("store: migrate %s status %s: %w", update.table, update.from, err)
		}
	}
	return nil
}

// migrateV7GoalOutcome adds the outcome column to goal_groups for existing DBs.
// Idempotent: isDuplicateColumn absorbs the error when the column already exists.
func (s *SQLiteStore) migrateV7GoalOutcome(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE goal_groups ADD COLUMN outcome TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumn(err) {
		return fmt.Errorf("store: migrate goal_groups outcome: %w", err)
	}
	return nil
}

// migrateV8FTSUpdateScope applies schema version 8: the tasks_fts_au AFTER
// UPDATE trigger gains a WHEN clause so it only rebuilds a task's FTS row when
// an FTS-indexed content column actually changes. Lease/heartbeat bumps
// (lease_expires) and other non-content edits no longer trigger an FTS
// delete+insert. Idempotent: DROP TRIGGER IF EXISTS then recreate.
func (s *SQLiteStore) migrateV8FTSUpdateScope(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS tasks_fts_au`); err != nil {
		return fmt.Errorf("store: drop tasks_fts_au: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE TRIGGER IF NOT EXISTS tasks_fts_au AFTER UPDATE ON tasks
WHEN (old.status<>new.status OR old.body<>new.body OR old.priority<>new.priority OR old.cwd<>new.cwd OR old.source<>new.source OR old.agent<>new.agent OR old.group_id<>new.group_id OR old.resource_key<>new.resource_key OR old.error<>new.error OR old.tags<>new.tags)
BEGIN
	DELETE FROM tasks_fts WHERE id = old.id;
	INSERT INTO tasks_fts(id, status, body, priority, cwd, source, agent, group_id, resource_key, error, tags)
	VALUES (new.id, new.status, new.body, new.priority, new.cwd, new.source, new.agent, new.group_id, new.resource_key, new.error, new.tags);
END;`); err != nil {
		return fmt.Errorf("store: recreate tasks_fts_au: %w", err)
	}
	return nil
}

// backfillTasksFTS repopulates the tasks_fts index from the tasks table during
// migration. It clears the index first so repeated migrations (or a version
// bump on an existing DB whose tasks_fts was created empty by IF NOT EXISTS)
// converge to one row per task. Idempotent: safe to run on every migration.
func (s *SQLiteStore) backfillTasksFTS(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tasks_fts`); err != nil {
		return fmt.Errorf("store: clear tasks_fts: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO tasks_fts(id, status, body, priority, cwd, source, agent, group_id, resource_key, error, tags)
SELECT id, status, body, priority, cwd, source, agent, group_id, resource_key, error, tags
FROM tasks`); err != nil {
		return fmt.Errorf("store: backfill tasks_fts: %w", err)
	}
	return nil
}
