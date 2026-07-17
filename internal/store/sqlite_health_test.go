package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueueHealthUsesBoundedWindowAndNullableAges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	_, err = s.db.ExecContext(ctx, `
INSERT INTO tasks(id,created,status,body,started,available_at,ordinal) VALUES
 ('ready','2025-01-02T10:00:00Z','todo','ready','','',1),
 ('future','2025-01-01T10:00:00Z','todo','future','','2025-01-03T00:00:00Z',2),
 ('active','2025-01-02T09:00:00Z','doing','active','2025-01-02T11:00:00Z','',3);
INSERT INTO task_events(task_id,type,at,message) VALUES
 ('active','requeued','2025-01-02T11:30:00Z','stale'),
 ('active','requeued','2025-01-01T11:00:00Z','stale'),
 ('active','requeued','2025-01-02T11:45:00Z','manual');
INSERT INTO task_attempts(task_id,started,finished,status) VALUES
 ('active','2025-01-02T10:00:00Z','2025-01-02T10:30:00Z','done'),
 ('active','2025-01-02T11:00:00Z','2025-01-02T11:30:00Z','failed');`)
	require.NoError(t, err)

	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	health, err := s.QueueHealth(ctx, now, 24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(86400), health.WindowSeconds)
	require.NotNil(t, health.OldestReadyAgeSeconds)
	require.Equal(t, int64(7200), *health.OldestReadyAgeSeconds)
	require.NotNil(t, health.OldestActiveAgeSeconds)
	require.Equal(t, int64(3600), *health.OldestActiveAgeSeconds)
	require.Equal(t, 1, health.StaleRequeues)
	require.Equal(t, 1, health.RetryAttempts)
	require.Equal(t, 2, health.TerminalAttempts)
	require.Equal(t, 1, health.TerminalFailures)
	require.NotNil(t, health.TerminalFailureRate)
	require.Equal(t, 0.5, *health.TerminalFailureRate)
}

func TestQueueHealthEmptyQueueUsesNulls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	health, err := s.QueueHealth(ctx, time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC), 24*time.Hour)
	require.NoError(t, err)
	require.Nil(t, health.OldestReadyAgeSeconds)
	require.Nil(t, health.OldestActiveAgeSeconds)
	require.Nil(t, health.TerminalFailureRate)
}

func TestQueueHealthIndexesExist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	for _, name := range []string{
		"tasks_status_created_idx",
		"tasks_status_started_idx",
		"task_events_stale_requeue_at_idx",
		"task_attempts_started_idx",
		"task_attempts_status_finished_idx",
	} {
		var count int
		require.NoError(t, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count))
		require.Equal(t, 1, count, name)
	}
}

func TestQueueHealthQueriesUseBoundedIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	tests := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{name: "oldest ready", query: oldestReadyHealthSQL, args: []any{"todo", "2025-01-02T12:00:00Z", "done", "doing"}, wantIndex: "tasks_status_created_idx"},
		{name: "oldest active", query: oldestActiveHealthSQL, args: []any{"doing"}, wantIndex: "tasks_status_started_idx"},
		{name: "stale requeues", query: staleRequeuesHealthSQL, args: []any{"2025-01-01T12:00:00Z"}, wantIndex: "task_events_stale_requeue_at_idx"},
		{name: "retry attempts", query: retryAttemptsHealthSQL, args: []any{"2025-01-01T12:00:00Z"}, wantIndex: "task_attempts_started_idx"},
		{name: "terminal attempts", query: terminalAttemptsHealthSQL, args: []any{"2025-01-01T12:00:00Z"}, wantIndex: "task_attempts_status_finished_idx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := explainPlan(t, s.db, tt.query, tt.args...)
			require.Contains(t, plan, tt.wantIndex)
			require.NotContains(t, plan, "USE TEMP B-TREE")
		})
	}
}

func explainPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		details = append(details, detail)
	}
	require.NoError(t, rows.Err())
	return strings.Join(details, "\n")
}
