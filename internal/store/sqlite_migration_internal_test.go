package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// TestMigrationRunsOnceAcrossOpens confirms that the second NewSQLite on an
// already-migrated DB performs zero UPDATE/ALTER on user tables. We verify
// indirectly with an AFTER UPDATE trigger installed before the second open
// that logs any update to a probe table; an empty probe table means no
// migration UPDATE traffic ran. The schema_version row written by the first
// open is also asserted.
func TestMigrationRunsOnceAcrossOpens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")

	// First open creates schema + records version 1.
	s1, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	version, err := s1.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, currentSchemaVersion, version)
	require.NoError(t, s1.Close())

	// Install a probe trigger on tasks BEFORE reopening so any migration
	// UPDATE on tasks would surface in the probe table.
	probeDSN := sqliteDSN(path)
	probeDB, err := sql.Open("sqlite", probeDSN)
	require.NoError(t, err)
	_, err = probeDB.ExecContext(ctx, `CREATE TABLE migration_probe (note TEXT)`)
	require.NoError(t, err)
	_, err = probeDB.ExecContext(ctx, `
CREATE TRIGGER probe_tasks_update AFTER UPDATE ON tasks
BEGIN
  INSERT INTO migration_probe (note) VALUES ('updated');
END;`)
	require.NoError(t, err)
	require.NoError(t, probeDB.Close())

	// Second open should be a no-op for migrations.
	s2, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	require.NoError(t, s2.Close())

	checkDB, err := sql.Open("sqlite", probeDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = checkDB.Close() })
	var count int
	require.NoError(t, checkDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM migration_probe`).Scan(&count))
	require.Zero(t, count, "second NewSQLite open ran UPDATE on tasks")
}

// TestMigrationAppliesOnFreshDBMissingMetadataRow confirms the fresh-DB
// path: a brand new file gets the schema_version row written even though
// there is nothing to migrate.
func TestMigrationAppliesOnFreshDBMissingMetadataRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	version, err := s.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, currentSchemaVersion, version)
}

func TestSchemaV12RejectsNewerDatabaseBeforeBootstrapMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	require.NoError(t, s.writeSchemaVersion(ctx, currentSchemaVersion+1))
	require.NoError(t, s.Close())

	raw, err := sql.Open("sqlite", sqliteDSN(path))
	require.NoError(t, err)
	_, err = raw.ExecContext(ctx, `DROP TABLE goal_iterations`)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	_, err = NewSQLite(ctx, Paths{SQLitePath: path})
	require.ErrorContains(t, err, "newer than supported")
	check, err := sql.Open("sqlite", sqliteDSN(path))
	require.NoError(t, err)
	t.Cleanup(func() { _ = check.Close() })
	var count int
	require.NoError(t, check.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='goal_iterations'`).Scan(&count))
	require.Zero(t, count, "forward-schema rejection must happen before bootstrap DDL")
}

func TestSchemaV11RevisionTriggerCatchesLegacyWriter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	require.NoError(t, s.Add(ctx, task.Task{ID: "legacy", Status: task.StatusTodo, Body: "before"}))
	require.NoError(t, s.Close())
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.ExecContext(ctx, `UPDATE tasks SET body='after' WHERE id='legacy'`)
	require.NoError(t, err)
	var revision int64
	require.NoError(t, raw.QueryRowContext(ctx, `SELECT revision FROM tasks WHERE id='legacy'`).Scan(&revision))
	require.Equal(t, int64(2), revision)
}

func TestSchemaV11UpgradeKeepsLegacyGoalsUnlimited(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx, `DROP TABLE goal_groups; CREATE TABLE goal_groups (id TEXT PRIMARY KEY, objective TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '', created TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT ''); INSERT INTO goal_groups(id,status,group_id) VALUES ('goal','active','goal')`)
	require.NoError(t, err)
	require.NoError(t, s.writeSchemaVersion(ctx, 10))
	require.NoError(t, s.Close())
	reopened, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	goal, err := reopened.GetGoalGroup(ctx, "goal")
	require.NoError(t, err)
	require.Zero(t, goal.MaxTokens)
	require.Zero(t, goal.MaxIterations)
	require.Zero(t, goal.MaxDuration)
}

// TestMigrationV5ToV6 confirms that a DB recorded at schema version 5 is
// migrated cleanly to version 6 on the next open. Version 6 is a no-op slot
// (StatusBudgetLimited adds no columns), so the only observable change is the
// recorded schema_version advancing to currentSchemaVersion.
func TestMigrationV5ToV6(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")

	// First open creates the schema; force the recorded version back to 5.
	s1, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	require.NoError(t, s1.writeSchemaVersion(ctx, 5))
	stale, err := s1.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, stale)
	require.NoError(t, s1.Close())

	// Reopen: the migration ladder must advance 5 -> currentSchemaVersion.
	s2, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })

	version, err := s2.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, currentSchemaVersion, version)
	require.Equal(t, 13, currentSchemaVersion)
}

// TestMigrationV7ToV8FTSUpdateScope confirms that a DB recorded at schema
// version 7 (with the old unconditional tasks_fts_au) is migrated to version 8
// and that the recreated trigger only rebuilds the FTS row when an
// FTS-indexed content column changes: a lease-only UPDATE must NOT re-index,
// an indexed-column UPDATE must re-index.
func TestMigrationV7ToV8FTSUpdateScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")

	// First open creates the schema; force the recorded version back to 7.
	s1, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	require.NoError(t, s1.writeSchemaVersion(ctx, 7))
	require.NoError(t, s1.Close())

	// Reopen: the migration ladder must advance 7 -> currentSchemaVersion.
	s2, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })

	version, err := s2.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, currentSchemaVersion, version)
	require.Equal(t, 13, currentSchemaVersion)

	// Drive raw UPDATEs through a probe connection and assert FTS contents
	// via the matchinfo-free count of rows whose body matches.
	probe, err := sql.Open("sqlite", sqliteDSN(path))
	require.NoError(t, err)
	t.Cleanup(func() { _ = probe.Close() })

	_, err = probe.ExecContext(ctx, `
INSERT INTO tasks (id, created, status, body, ordinal)
VALUES ('v8', '2026-01-01T00:00:00Z', 'todo', 'alpha keyword body', 1)`)
	require.NoError(t, err)

	countMatch := func(term string) int {
		var n int
		require.NoError(t, probe.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks_fts WHERE tasks_fts MATCH ?`, term).Scan(&n))
		return n
	}

	// Baseline: original body is indexed (AI trigger).
	require.Equal(t, 1, countMatch("alpha"))

	// Lease-only UPDATE (non-indexed column) must NOT rebuild FTS: the row
	// stays matchable by its original body and gains no new content.
	_, err = probe.ExecContext(ctx,
		`UPDATE tasks SET lease_expires = '2026-02-02T00:00:00Z' WHERE id = 'v8'`)
	require.NoError(t, err)
	require.Equal(t, 1, countMatch("alpha"))

	// Indexed-column UPDATE (body) must rebuild FTS: old term gone, new term present.
	_, err = probe.ExecContext(ctx,
		`UPDATE tasks SET body = 'omega keyword body' WHERE id = 'v8'`)
	require.NoError(t, err)
	require.Equal(t, 0, countMatch("alpha"))
	require.Equal(t, 1, countMatch("omega"))
}

func TestMigrationV11ToV12AddsAvailableAtAndPreservesIntegrity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	require.NoError(t, s.Add(ctx, task.Task{ID: "legacy", Status: task.StatusTodo, Body: "legacy"}))
	require.NoError(t, s.writeSchemaVersion(ctx, 11))
	_, err = s.db.ExecContext(ctx, `DROP INDEX tasks_status_available_order_idx; ALTER TABLE tasks DROP COLUMN available_at`)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	reopened, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.Get(ctx, "legacy")
	require.NoError(t, err)
	require.Empty(t, got.AvailableAt)
	version, err := reopened.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, currentSchemaVersion, version)
	var integrity string
	require.NoError(t, reopened.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity))
	require.Equal(t, "ok", integrity)
}

func TestMigrationV12ToV13AddsQueueHealthIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	require.NoError(t, s.writeSchemaVersion(ctx, 12))
	for _, name := range []string{
		"tasks_status_created_idx",
		"tasks_status_started_idx",
		"task_events_stale_requeue_at_idx",
		"task_attempts_started_idx",
		"task_attempts_status_finished_idx",
	} {
		_, err = s.db.ExecContext(ctx, `DROP INDEX `+name)
		require.NoError(t, err)
	}
	require.NoError(t, s.Close())

	reopened, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	version, err := reopened.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, 13, version)
	for _, name := range []string{
		"tasks_status_created_idx",
		"tasks_status_started_idx",
		"task_events_stale_requeue_at_idx",
		"task_attempts_started_idx",
		"task_attempts_status_finished_idx",
	} {
		var count int
		require.NoError(t, reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count))
		require.Equal(t, 1, count, name)
	}
}

func TestMigrationIsDuplicateColumnTypedError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "tasks.sqlite")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.ExecContext(ctx, `CREATE TABLE dup (a TEXT)`)
	require.NoError(t, err)

	// A fresh, non-conflicting ADD COLUMN must succeed (not a duplicate).
	_, err = db.ExecContext(ctx, `ALTER TABLE dup ADD COLUMN b TEXT`)
	require.NoError(t, err)
	require.False(t, isDuplicateColumn(err))

	// Re-adding the same column yields the duplicate-column error, which
	// isDuplicateColumn must recognize via the typed *sqlite.Error path.
	_, dupErr := db.ExecContext(ctx, `ALTER TABLE dup ADD COLUMN b TEXT`)
	require.Error(t, dupErr)
	require.True(t, isDuplicateColumn(dupErr))

	// Wrapped duplicate-column errors are still recognized (errors.As walks
	// the chain) and non-sqlite errors are rejected.
	require.True(t, isDuplicateColumn(fmt.Errorf("store: migrate b: %w", dupErr)))
	require.False(t, isDuplicateColumn(errors.New("some other error")))
	require.False(t, isDuplicateColumn(nil))
}

func TestMigrationLegacyTaskDependenciesMissingRelationType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")

	// Build a legacy-shaped DB on a raw connection: task_dependencies WITHOUT
	// the relation_type column, and WITHOUT the task_dependencies_type_idx that
	// references it. No schema_version row, so NewSQLite runs the full ladder.
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	require.NoError(t, err)
	_, err = raw.ExecContext(ctx, `
CREATE TABLE task_dependencies (
	task_id TEXT NOT NULL,
	depends_on_id TEXT NOT NULL,
	created TEXT NOT NULL,
	PRIMARY KEY (task_id, depends_on_id)
);`)
	require.NoError(t, err)
	// Sanity: the type index must not exist yet, and CREATE INDEX on the
	// missing column must fail — this is the exact failure NewSQLite hit.
	_, idxErr := raw.ExecContext(ctx,
		`CREATE INDEX task_dependencies_type_idx ON task_dependencies(task_id, relation_type)`)
	require.Error(t, idxErr, "precondition: relation_type must be absent so the legacy index fails")
	require.NoError(t, raw.Close())

	// Opening the legacy DB must succeed: migrateTaskMetadata adds
	// relation_type, then migrateTaskDependencyTypeIndex creates the index.
	s, err := NewSQLite(ctx, Paths{SQLitePath: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	version, err := s.readSchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, currentSchemaVersion, version)

	// The relation_type column and the type index must both be present.
	check, err := sql.Open("sqlite", sqliteDSN(path))
	require.NoError(t, err)
	t.Cleanup(func() { _ = check.Close() })

	var colCount int
	require.NoError(t, check.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('task_dependencies') WHERE name = 'relation_type'`).Scan(&colCount))
	require.Equal(t, 1, colCount, "relation_type column must be added on legacy upgrade")

	var idxCount int
	require.NoError(t, check.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'task_dependencies_type_idx'`).Scan(&idxCount))
	require.Equal(t, 1, idxCount, "task_dependencies_type_idx must end up present after legacy upgrade")
}
