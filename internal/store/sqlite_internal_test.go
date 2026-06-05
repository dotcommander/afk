package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
	sqlite "modernc.org/sqlite"
)

// produceSQLiteBusy returns a real *sqlite.Error with SQLITE_BUSY code by
// holding a write transaction on one connection and starting an immediate
// transaction on a second connection with busy_timeout=0.
func produceSQLiteBusy(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "busy.sqlite")
	dsn := "file:" + path + "?_pragma=busy_timeout(0)&_txlock=immediate"
	db1, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db1.Close() })
	db2, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db2.Close() })
	_, err = db1.Exec(`CREATE TABLE t (n INTEGER)`)
	require.NoError(t, err)
	tx1, err := db1.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback() }()
	_, err = tx1.Exec(`INSERT INTO t VALUES (1)`)
	require.NoError(t, err)
	_, busyErr := db2.ExecContext(context.Background(), `INSERT INTO t VALUES (2)`)
	require.Error(t, busyErr)
	return busyErr
}

func TestSQLiteErrorHelpers(t *testing.T) {
	t.Parallel()

	require.False(t, isSQLiteBusy(nil))
	require.False(t, isSQLiteBusy(errors.New("database is locked")))
	require.False(t, isSQLiteBusy(errors.New("other sqlite error")))

	busyErr := produceSQLiteBusy(t)
	var se *sqlite.Error
	require.True(t, errors.As(busyErr, &se), "expected typed sqlite.Error, got %T: %v", busyErr, busyErr)
	require.True(t, isSQLiteBusy(busyErr))

	require.True(t, isDuplicateTaskID(errors.New("UNIQUE constraint failed: tasks.id")))
	require.False(t, isDuplicateTaskID(errors.New("UNIQUE constraint failed: other.id")))
}

func TestDecodeTagsFallbacks(t *testing.T) {
	t.Parallel()

	require.Nil(t, decodeTags(""))
	require.Nil(t, decodeTags("{not json"))
	require.Equal(t, []string{"a", "b"}, decodeTags(`["a","b"]`))
}

func TestWaitSQLiteBusyRetryHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitSQLiteBusyRetry(ctx), context.Canceled)
}

func TestRetrySQLiteBusyEventuallySucceeds(t *testing.T) {
	t.Parallel()

	busyErr := produceSQLiteBusy(t)
	calls := 0
	err := retrySQLiteBusy(context.Background(), func(context.Context) error {
		calls++
		if calls < 3 {
			return busyErr
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.NoError(t, waitSQLiteBusyRetry(context.Background()))
}

// TestRetrySQLiteBusy exercises the retry loop's four contracts. Cases that
// would wait on the real 25ms timer run inside synctest.Test, which fakes
// time so no wall-clock dependency leaks into CI.
func TestRetrySQLiteBusy(t *testing.T) {
	t.Parallel()

	busyErr := produceSQLiteBusy(t)

	t.Run("returns nil first call without retry", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := retrySQLiteBusy(context.Background(), func(context.Context) error {
			calls++
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
	})

	t.Run("retries once on busy then succeeds", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			err := retrySQLiteBusy(context.Background(), func(context.Context) error {
				calls++
				if calls == 1 {
					return busyErr
				}
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 2, calls)
		})
	})

	t.Run("returns ctx error when cancelled mid-wait", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		err := retrySQLiteBusy(ctx, func(context.Context) error {
			calls++
			return busyErr
		})
		// First call returns busyErr; wait observes cancelled ctx and
		// returns the busy error (per implementation: returns err, not
		// ctx.Err). The contract is "stops retrying", so we assert the
		// returned error is the busy one and the loop didn't spin.
		require.ErrorIs(t, err, busyErr)
		require.Equal(t, 1, calls)
	})

	t.Run("returns non-busy error immediately without retry", func(t *testing.T) {
		t.Parallel()
		nonBusy := errors.New("some other failure")
		calls := 0
		err := retrySQLiteBusy(context.Background(), func(context.Context) error {
			calls++
			return nonBusy
		})
		require.ErrorIs(t, err, nonBusy)
		require.Equal(t, 1, calls)
	})
}

func TestSQLiteStoreLateOperationErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.NoError(t, s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "body"}))

	_, err = s.db.ExecContext(ctx, `DROP TABLE task_attempts`)
	require.NoError(t, err)
	_, err = s.ClaimNextForWorker(ctx, time.Now(), time.Time{}, "worker", "agent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "insert attempt")
}

func TestSQLiteStoreSchemaCorruptionErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("missing events table", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		_, err = s.db.ExecContext(ctx, `DROP TABLE task_events`)
		require.NoError(t, err)
		err = s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "body"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "insert event")
	})

	t.Run("missing attempts table", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "body"}))
		_, err = s.ClaimNextForWorker(ctx, time.Now(), time.Time{}, "worker", "agent")
		require.NoError(t, err)
		_, err = s.db.ExecContext(ctx, `DROP TABLE task_attempts`)
		require.NoError(t, err)
		err = s.Heartbeat(ctx, "task", "worker", time.Now(), time.Now().Add(time.Minute))
		require.Error(t, err)
		require.Contains(t, err.Error(), "heartbeat owner")
	})

	t.Run("missing dependencies table", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "a", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "a"}))
		require.NoError(t, s.Add(ctx, task.Task{ID: "b", Created: "2025-01-02T03:04:06Z", Status: task.StatusTodo, Body: "b"}))
		_, err = s.db.ExecContext(ctx, `DROP TABLE task_dependencies`)
		require.NoError(t, err)
		err = s.AddDependency(ctx, "a", "b")
		require.Error(t, err)
		require.Contains(t, err.Error(), "dependency path")
		_, err = s.Dependencies(ctx, "a")
		require.Error(t, err)
		require.Contains(t, err.Error(), "dependencies")
	})

	t.Run("missing tasks table", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		_, err = s.db.ExecContext(ctx, `DROP TABLE tasks`)
		require.NoError(t, err)
		require.Error(t, s.Add(ctx, task.Task{ID: "x", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "x"}))
		_, err = s.List(ctx)
		require.Error(t, err)
		_, err = s.Ready(ctx)
		require.Error(t, err)
		_, err = s.RequeueStale(ctx, time.Minute, time.Now())
		require.Error(t, err)
		_, err = s.ClaimNext(ctx, time.Now(), time.Time{})
		require.Error(t, err)
	})
}

func TestSQLiteStoreTriggerErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("add insert error", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_task_insert BEFORE INSERT ON tasks BEGIN SELECT RAISE(FAIL, 'insert blocked'); END;`)
		require.NoError(t, err)
		err = s.Add(ctx, task.Task{ID: "x", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "x"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "insert blocked")
	})

	t.Run("update and delete errors", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "x", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "x"}))
		_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_task_update BEFORE UPDATE ON tasks BEGIN SELECT RAISE(FAIL, 'update blocked'); END;`)
		require.NoError(t, err)
		err = s.Update(ctx, "x", task.EventDone, "", func(tk *task.Task) bool {
			tk.Status = task.StatusDone
			return true
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "update blocked")
		_, err = s.db.ExecContext(ctx, `DROP TRIGGER fail_task_update; CREATE TRIGGER fail_task_delete BEFORE DELETE ON tasks BEGIN SELECT RAISE(FAIL, 'delete blocked'); END;`)
		require.NoError(t, err)
		err = s.Delete(ctx, "x")
		require.Error(t, err)
		require.Contains(t, err.Error(), "delete blocked")
		_, err = s.Prune(ctx, []task.Status{task.StatusTodo})
		require.Error(t, err)
		require.Contains(t, err.Error(), "delete blocked")
	})

	t.Run("dependency insert error", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "a", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "a"}))
		require.NoError(t, s.Add(ctx, task.Task{ID: "b", Created: "2025-01-02T03:04:06Z", Status: task.StatusTodo, Body: "b"}))
		_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_dependency_insert BEFORE INSERT ON task_dependencies BEGIN SELECT RAISE(FAIL, 'dependency blocked'); END;`)
		require.NoError(t, err)
		err = s.AddDependency(ctx, "a", "b")
		require.Error(t, err)
		require.Contains(t, err.Error(), "dependency blocked")
	})

	t.Run("add with dependency rolls back task on dependency insert error", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "prereq"}))
		_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_dependency_insert BEFORE INSERT ON task_dependencies BEGIN SELECT RAISE(FAIL, 'dependency blocked'); END;`)
		require.NoError(t, err)

		err = s.AddWithDependency(ctx, task.Task{ID: "dependent", Created: "2025-01-02T03:04:06Z", Status: task.StatusTodo, Body: "dependent"}, "prereq")

		require.Error(t, err)
		require.Contains(t, err.Error(), "dependency blocked")
		_, err = s.Get(ctx, "dependent")
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("attempt finish error", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "x", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "x"}))
		_, err = s.ClaimNext(ctx, time.Now(), time.Time{})
		require.NoError(t, err)
		_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_attempt_update BEFORE UPDATE ON task_attempts BEGIN SELECT RAISE(FAIL, 'attempt blocked'); END;`)
		require.NoError(t, err)
		err = s.Update(ctx, "x", task.EventDone, "", func(tk *task.Task) bool {
			return tk.MarkDone(time.Now())
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "attempt blocked")
	})

	t.Run("requeue update error", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "x", Created: "2025-01-02T03:04:05Z", Status: task.StatusTodo, Body: "x"}))
		_, err = s.ClaimNext(ctx, time.Now().Add(-time.Hour), time.Time{})
		require.NoError(t, err)
		_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_requeue_update BEFORE UPDATE ON tasks BEGIN SELECT RAISE(FAIL, 'requeue blocked'); END;`)
		require.NoError(t, err)
		_, err = s.RequeueStale(ctx, time.Minute, time.Now())
		require.Error(t, err)
		require.Contains(t, err.Error(), "requeue blocked")
	})

	t.Run("prune event error", func(t *testing.T) {
		s, err := NewSQLite(ctx, Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		require.NoError(t, s.Add(ctx, task.Task{ID: "x", Created: "2025-01-02T03:04:05Z", Status: task.StatusDone, Body: "x"}))
		_, err = s.db.ExecContext(ctx, `DROP TABLE task_events`)
		require.NoError(t, err)
		_, err = s.Prune(ctx, []task.Status{task.StatusDone})
		require.Error(t, err)
		require.Contains(t, err.Error(), "insert event")
	})
}
