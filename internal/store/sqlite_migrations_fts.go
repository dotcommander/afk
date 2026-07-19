package store

import (
	"context"
	"fmt"
)

// migrateV6BudgetLimited applies schema version 6: the StatusBudgetLimited task
// status (the status column is already TEXT, so no column change is needed) and
// the goal_groups table. CREATE TABLE IF NOT EXISTS keeps it idempotent so
// fresh-DB init and an upgrade from v5 both converge to the same schema.
func (s *SQLiteStore) migrateV6BudgetLimited(ctx context.Context) error {
	if _, err := s.schemaExecer().ExecContext(ctx, `
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
		if _, err := s.schemaExecer().ExecContext(ctx, update.query, update.to, update.from); err != nil {
			return fmt.Errorf("store: migrate %s status %s: %w", update.table, update.from, err)
		}
	}
	return nil
}

// migrateV7GoalOutcome adds the outcome column to goal_groups for existing DBs.
// Idempotent: isDuplicateColumn absorbs the error when the column already exists.
func (s *SQLiteStore) migrateV7GoalOutcome(ctx context.Context) error {
	if _, err := s.schemaExecer().ExecContext(ctx, `ALTER TABLE goal_groups ADD COLUMN outcome TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumn(err) {
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
	if _, err := s.schemaExecer().ExecContext(ctx, `DROP TRIGGER IF EXISTS tasks_fts_au`); err != nil {
		return fmt.Errorf("store: drop tasks_fts_au: %w", err)
	}
	if _, err := s.schemaExecer().ExecContext(ctx, `
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
	if _, err := s.schemaExecer().ExecContext(ctx, `DELETE FROM tasks_fts`); err != nil {
		return fmt.Errorf("store: clear tasks_fts: %w", err)
	}
	if _, err := s.schemaExecer().ExecContext(ctx, `
INSERT INTO tasks_fts(id, status, body, priority, cwd, source, agent, group_id, resource_key, error, tags)
SELECT id, status, body, priority, cwd, source, agent, group_id, resource_key, error, tags
FROM tasks`); err != nil {
		return fmt.Errorf("store: backfill tasks_fts: %w", err)
	}
	return nil
}
