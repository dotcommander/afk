package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

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
	if err := s.migrateV12AvailableAt(ctx); err != nil {
		return err
	}
	if err := s.migrateV13QueueHealthIndexes(ctx); err != nil {
		return err
	}
	return s.writeSchemaVersion(ctx, currentSchemaVersion)
}

func (s *SQLiteStore) migrateV13QueueHealthIndexes(ctx context.Context) error {
	if _, err := s.schemaExecer().ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS tasks_status_created_idx ON tasks(status, created);
CREATE INDEX IF NOT EXISTS tasks_status_started_idx ON tasks(status, started);
CREATE INDEX IF NOT EXISTS task_events_stale_requeue_at_idx ON task_events(at) WHERE type='requeued' AND message='stale';
CREATE INDEX IF NOT EXISTS task_attempts_started_idx ON task_attempts(started, task_id, id);
CREATE INDEX IF NOT EXISTS task_attempts_status_finished_idx ON task_attempts(status, finished);`); err != nil {
		return fmt.Errorf("store: migrate queue health indexes: %w", err)
	}
	return nil
}

func (s *SQLiteStore) migrateV12AvailableAt(ctx context.Context) error {
	if _, err := s.schemaExecer().ExecContext(ctx, `ALTER TABLE tasks ADD COLUMN available_at TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumn(err) {
		return fmt.Errorf("store: migrate available_at: %w", err)
	}
	if _, err := s.schemaExecer().ExecContext(ctx, `CREATE INDEX IF NOT EXISTS tasks_status_available_order_idx ON tasks(status, available_at, ordinal)`); err != nil {
		return fmt.Errorf("store: migrate available_at index: %w", err)
	}
	return nil
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
		if _, err := s.schemaExecer().ExecContext(ctx, statement); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("store: migrate durable goal column: %w", err)
		}
	}
	if _, err := s.schemaExecer().ExecContext(ctx, `
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
		if _, err := s.schemaExecer().ExecContext(ctx, statement); err != nil && (i != 0 || !strings.Contains(err.Error(), "duplicate column name")) {
			return fmt.Errorf("store: migrate retirement state: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) migrateV9RequestLedger(ctx context.Context) error {
	if _, err := s.schemaExecer().ExecContext(ctx, `
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
	err := s.schemaExecer().QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = ?`, schemaVersionKey).Scan(&raw)
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
	if _, err := s.schemaExecer().ExecContext(ctx,
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
		if _, err := s.schemaExecer().ExecContext(ctx, col.sql); err != nil && !isDuplicateColumn(err) {
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
	if _, err := s.schemaExecer().ExecContext(ctx, `CREATE INDEX IF NOT EXISTS task_dependencies_type_idx ON task_dependencies(task_id, relation_type)`); err != nil {
		return fmt.Errorf("store: migrate task_dependencies_type_idx: %w", err)
	}
	return nil
}
