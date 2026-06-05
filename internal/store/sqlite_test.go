package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.NewSQLite(context.Background(), store.Paths{
		SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

func TestSQLiteStoreAddListUpdateDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusTodo, Body: "one"}))
	require.ErrorIs(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusTodo, Body: "dupe"}), store.ErrDuplicateTask)
	require.NoError(t, s.Update(ctx, "1", task.EventFailed, "", func(tk *task.Task) bool {
		tk.Body = "two"
		return true
	}))

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "two", tasks[0].Body)

	require.NoError(t, s.Delete(ctx, "1"))
	tasks, err = s.List(ctx)
	require.NoError(t, err)
	require.Empty(t, tasks)
}

func TestSQLiteStoreMutationEdgeCases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.ErrorIs(t, s.Delete(ctx, "missing"), store.ErrNotFound)
	_, err := s.Prune(ctx, []task.Status{task.Status("bogus")})
	require.ErrorIs(t, err, task.ErrInvalidStatus)
	require.ErrorIs(t, s.AddDependency(ctx, "", "x"), store.ErrInvalidDependency)
	require.ErrorIs(t, s.Update(ctx, "missing", task.EventDone, "", func(*task.Task) bool { return true }), store.ErrNotFound)

	require.NoError(t, s.Add(ctx, task.Task{ID: "noop", Status: task.StatusTodo, Body: "noop"}))
	require.NoError(t, s.Update(ctx, "noop", task.EventDone, "", func(*task.Task) bool { return false }))
	events, err := s.Events(ctx, "noop")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, task.EventAdded, events[0].Type)

	require.NoError(t, s.Add(ctx, task.Task{ID: "working-without-attempt", Status: task.StatusDoing, Body: "working"}))
	require.ErrorIs(t, s.Heartbeat(ctx, "working-without-attempt", "worker", time.Now(), time.Now().Add(time.Minute)), store.ErrInvalidState)
}

func TestSQLiteStoreNewSQLiteReportsInvalidParentPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(filePath, nil, 0o600))

	_, err := store.NewSQLite(ctx, store.Paths{SQLitePath: filepath.Join(filePath, "tasks.sqlite")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mkdir sqlite dir")
}

func TestSQLiteStoreNewSQLiteReportsCanceledInit(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.NewSQLite(ctx, store.Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "create schema")
}

func TestSQLiteStoreMethodsReportClosedDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = s.List(ctx)
	require.Error(t, err)
	_, err = s.Ready(ctx)
	require.Error(t, err)
	_, err = s.Events(ctx, "missing")
	require.Error(t, err)
	_, err = s.Attempts(ctx, "missing")
	require.Error(t, err)
	_, err = s.RequeueStale(ctx, time.Minute, time.Now())
	require.Error(t, err)
	require.Error(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusTodo, Body: "one"}))
	require.Error(t, s.Update(ctx, "1", task.EventDone, "", func(*task.Task) bool { return true }))
	require.Error(t, s.Delete(ctx, "1"))
	_, err = s.Prune(ctx, []task.Status{task.StatusDone})
	require.Error(t, err)
	_, err = s.ClaimNext(ctx, time.Now(), time.Time{})
	require.Error(t, err)
	require.Error(t, s.Heartbeat(ctx, "1", "worker", time.Now(), time.Now().Add(time.Minute)))
	require.Error(t, s.AddDependency(ctx, "1", "2"))
	_, err = s.Dependencies(ctx, "1")
	require.Error(t, err)
}

func TestSQLiteStoreMigratesLegacyStatusNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := sql.Open("sqlite", store.ResolvePaths(dbPath).SQLitePath)
	require.NoError(t, err)
	_, err = db.Exec(`
CREATE TABLE tasks (
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
CREATE TABLE task_attempts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	started TEXT NOT NULL,
	finished TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	worker_id TEXT NOT NULL DEFAULT '',
	agent TEXT NOT NULL DEFAULT ''
);
INSERT INTO tasks (id, created, status, body, ordinal) VALUES ('old-pending', '2025-01-02T03:04:05Z', 'pending', 'body', 1);
INSERT INTO tasks (id, created, status, body, ordinal) VALUES ('old-working', '2025-01-02T03:04:05Z', 'working', 'body', 2);
INSERT INTO task_attempts (task_id, started, status) VALUES ('old-working', '2025-01-02T03:04:05Z', 'working');
`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, tasks[0].Status)
	require.Equal(t, task.StatusDoing, tasks[1].Status)
	attempts, err := s.Attempts(ctx, "old-working")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, task.StatusDoing, attempts[0].Status)
}

func TestSQLiteStoreConcurrentFirstOpenAndAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	paths := store.Paths{SQLitePath: filepath.Join(dir, "tasks.sqlite")}
	const workers = 20
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s, err := store.NewSQLite(ctx, paths)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close() //nolint:errcheck // test reports operation errors explicitly
			id := fmt.Sprintf("concurrent-%02d", i)
			errs <- s.Add(ctx, task.Task{ID: id, Status: task.StatusTodo, Body: id})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	s, err := store.NewSQLite(ctx, paths)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, workers)
}

func TestSQLiteStorePersistsMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{
		ID:          "meta",
		Status:      task.StatusTodo,
		Body:        "body",
		Priority:    "high",
		Tags:        []string{"repo:afk", "type:test"},
		CWD:         "/tmp/repo",
		Source:      "cli",
		Agent:       "codex",
		GroupID:     "group",
		ResourceKey: "repo:/tmp/repo",
	}))

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, task.PriorityHigh, tasks[0].Priority)
	require.Equal(t, []string{"repo:afk", "type:test"}, tasks[0].Tags)
	require.Equal(t, "/tmp/repo", tasks[0].CWD)
	require.Equal(t, "cli", tasks[0].Source)
	require.Equal(t, "codex", tasks[0].Agent)
	require.Equal(t, "group", tasks[0].GroupID)
	require.Equal(t, "repo:/tmp/repo", tasks[0].ResourceKey)
}

func TestSQLiteStoreClaimNext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "done", Status: task.StatusDone, Body: "done"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusTodo, Body: "pending"}))

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "pending", claimed.ID)
	require.Equal(t, task.StatusDoing, claimed.Status)
	require.Equal(t, "2025-01-02T03:04:05Z", claimed.Started)

	claimed, err = s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.Nil(t, claimed)
}

func TestSQLiteStoreReadyReturnsPendingTasksInClaimOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "done", Status: task.StatusDone, Body: "done"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusTodo, Body: "first"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "failed", Status: task.StatusFailed, Body: "failed"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusTodo, Body: "second"}))

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 2)
	require.Equal(t, "first", ready[0].ID)
	require.Equal(t, "second", ready[1].ID)
}

func TestSQLiteStoreReadyOrdersByPriority(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	for _, tsk := range []task.Task{
		{ID: "normal", Status: task.StatusTodo, Body: "normal"},
		{ID: "low", Status: task.StatusTodo, Body: "low", Priority: "low"},
		{ID: "unknown", Status: task.StatusTodo, Body: "unknown", Priority: "later"},
		{ID: "urgent", Status: task.StatusTodo, Body: "urgent", Priority: " urgent "},
		{ID: "high", Status: task.StatusTodo, Body: "high", Priority: "HIGH"},
	} {
		require.NoError(t, s.Add(ctx, tsk))
	}

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	requireIDs(t, ready, "urgent", "high", "normal", "unknown", "low")
}

func TestSQLiteStoreClaimNextOrdersByPriority(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "normal", Status: task.StatusTodo, Body: "normal"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "urgent", Status: task.StatusTodo, Body: "urgent", Priority: "urgent"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "high", Status: task.StatusTodo, Body: "high", Priority: "high"}))

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "urgent", claimed.ID)

	claimed, err = s.ClaimNext(ctx, now.Add(time.Second), time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "high", claimed.ID)
}

func TestSQLiteStorePriorityDoesNotBypassReadinessConstraints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked-by-dep", Status: task.StatusTodo, Body: "blocked", Priority: "urgent"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusTodo, Body: "prereq"}))
	require.NoError(t, s.AddDependency(ctx, "blocked-by-dep", "prereq"))

	require.NoError(t, s.Add(ctx, task.Task{ID: "resource-active", Status: task.StatusDoing, Body: "active", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "resource-blocked", Status: task.StatusTodo, Body: "blocked", Priority: "urgent", ResourceKey: "repo:x"}))

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	requireIDs(t, ready, "prereq")
}

func TestSQLiteStoreReadyAgreesWithClaimNext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "done", Status: task.StatusDone, Body: "done"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusTodo, Body: "first"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusTodo, Body: "second"}))

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 2)

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, ready[0].ID, claimed.ID)
}

func TestSQLiteStoreReadyExcludesUnfinishedDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusTodo, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusTodo, Body: "prereq"}))
	require.NoError(t, s.AddDependency(ctx, "blocked", "prereq"))

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 1)
	require.Equal(t, "prereq", ready[0].ID)
}

func TestSQLiteStoreReadyExcludesMissingDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusTodo, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusTodo, Body: "prereq"}))
	require.NoError(t, s.AddDependency(ctx, "blocked", "prereq"))
	require.NoError(t, s.Delete(ctx, "prereq"))

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	require.Empty(t, ready)

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.Nil(t, claimed)
}

func TestSQLiteStoreDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusTodo, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusTodo, Body: "prereq"}))

	require.NoError(t, s.AddDependency(ctx, "blocked", "prereq"))
	deps, err := s.Dependencies(ctx, "blocked")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, "blocked", deps[0].TaskID)
	require.Equal(t, "prereq", deps[0].DependsOnID)
	require.NotEmpty(t, deps[0].Created)

	events, err := s.Events(ctx, "blocked")
	require.NoError(t, err)
	require.Equal(t, task.EventDependencyAdded, events[len(events)-1].Type)
	require.Equal(t, "prereq", events[len(events)-1].Message)

	eventCount := len(events)
	require.NoError(t, s.AddDependency(ctx, "blocked", "prereq"))
	events, err = s.Events(ctx, "blocked")
	require.NoError(t, err)
	require.Len(t, events, eventCount)

	deps, err = s.Dependencies(ctx, "blocked")
	require.NoError(t, err)
	require.Len(t, deps, 1)
}

func TestSQLiteStoreDependencyValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "a", Status: task.StatusTodo, Body: "a"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "b", Status: task.StatusTodo, Body: "b"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "c", Status: task.StatusTodo, Body: "c"}))

	require.ErrorIs(t, s.AddDependency(ctx, "a", "a"), task.ErrInvalidRelation)
	require.ErrorIs(t, s.AddDependency(ctx, "a", "missing"), store.ErrNotFound)
	require.ErrorIs(t, s.AddDependency(ctx, "missing", "a"), store.ErrNotFound)

	require.NoError(t, s.AddDependency(ctx, "b", "a"))
	require.NoError(t, s.AddDependency(ctx, "c", "b"))
	require.ErrorIs(t, s.AddDependency(ctx, "a", "c"), store.ErrDependencyCycle)
}

func TestSQLiteStoreDependenciesAffectClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusTodo, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusTodo, Body: "prereq"}))
	require.NoError(t, s.AddDependency(ctx, "blocked", "prereq"))

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "prereq", claimed.ID)
	require.NoError(t, s.Update(ctx, "prereq", task.EventDone, "", func(tk *task.Task) bool {
		return tk.MarkDone(now.Add(time.Minute))
	}))

	claimed, err = s.ClaimNext(ctx, now.Add(2*time.Minute), time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "blocked", claimed.ID)
}

func TestSQLiteStoreResourceLocksAffectClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusTodo, Body: "first", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusTodo, Body: "second", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "other", Status: task.StatusTodo, Body: "other", ResourceKey: "repo:y"}))

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "first", claimed.ID)

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 1)
	require.Equal(t, "other", ready[0].ID)

	claimed, err = s.ClaimNext(ctx, now.Add(time.Second), time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "other", claimed.ID)

	claimed, err = s.ClaimNext(ctx, now.Add(2*time.Second), time.Time{})
	require.NoError(t, err)
	require.Nil(t, claimed)
}

func TestSQLiteStoreExpiredLeaseRequeueReleasesResourceLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusTodo, Body: "first", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusTodo, Body: "second", ResourceKey: "repo:x"}))
	_, err := s.ClaimNext(ctx, now.Add(-time.Hour), now.Add(-30*time.Minute))
	require.NoError(t, err)

	ready, err := s.Ready(ctx)
	require.NoError(t, err)
	require.Empty(t, ready)

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.Nil(t, claimed)

	_, err = s.RequeueStale(ctx, time.Minute, now)
	require.NoError(t, err)

	ready, err = s.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 2)
	require.Equal(t, "first", ready[0].ID)
	require.Equal(t, "second", ready[1].ID)
}

func TestSQLiteStoreClaimNextWithLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusTodo, Body: "pending"}))
	claimed, err := s.ClaimNext(ctx, now, now.Add(30*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "2025-01-02T03:34:05Z", claimed.LeaseExpires)
}

func TestSQLiteStoreClaimNextForWorkerRecordsAttemptOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusTodo, Body: "pending"}))
	claimed, err := s.ClaimNextForWorker(ctx, now, time.Time{}, "worker-1", "codex")
	require.NoError(t, err)
	require.NotNil(t, claimed)

	attempts, err := s.Attempts(ctx, "pending")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, "worker-1", attempts[0].WorkerID)
	require.Equal(t, "codex", attempts[0].Agent)
}

func TestSQLiteStoreHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusTodo, Body: "pending"}))
	_, err := s.ClaimNextForWorker(ctx, now, now.Add(time.Minute), "worker-1", "codex")
	require.NoError(t, err)

	require.ErrorIs(t, s.Heartbeat(ctx, "pending", "worker-2", now, now.Add(30*time.Minute)), store.ErrWorkerMismatch)
	require.NoError(t, s.Heartbeat(ctx, "pending", "worker-1", now.Add(time.Second), now.Add(30*time.Minute)))

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, "2025-01-02T03:34:05Z", tasks[0].LeaseExpires)

	events, err := s.Events(ctx, "pending")
	require.NoError(t, err)
	require.Equal(t, task.EventHeartbeat, events[len(events)-1].Type)
	require.Equal(t, "worker-1", events[len(events)-1].Message)
}

func TestSQLiteStoreHeartbeatRequiresWorkingTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusTodo, Body: "pending"}))
	require.ErrorIs(t, s.Heartbeat(ctx, "pending", "worker-1", now, now.Add(30*time.Minute)), store.ErrInvalidState)
}

func TestSQLiteStoreClaimNextEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	claimed, err := s.ClaimNext(ctx, time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), time.Time{})
	require.NoError(t, err)
	require.Nil(t, claimed)
}

func TestSQLiteStoreConcurrentClaimNextDoesNotDuplicateClaims(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	for i := range 10 {
		id := fmt.Sprintf("task-%02d", i)
		require.NoError(t, s.Add(ctx, task.Task{ID: id, Status: task.StatusTodo, Body: id}))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := map[string]bool{}
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := s.ClaimNext(ctx, now.Add(time.Duration(i)*time.Second), time.Time{})
			if err != nil {
				errs <- err
				return
			}
			if got == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if claimed[got.ID] {
				errs <- fmt.Errorf("duplicate claim %s", got.ID)
				return
			}
			claimed[got.ID] = true
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, claimed, 10)
}

func TestSQLiteStoreRecordsEventsAndAttempts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{
		ID:      "task",
		Created: "2025-01-02T03:00:00Z",
		Status:  task.StatusTodo,
		Body:    "body",
	}))
	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.Equal(t, "task", claimed.ID)
	require.NoError(t, s.Update(ctx, "task", task.EventFailed, "boom", func(tk *task.Task) bool {
		return tk.MarkFailed(now.Add(time.Minute), "boom")
	}))

	events, err := s.Events(ctx, "task")
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.Equal(t, task.EventAdded, events[0].Type)
	require.Equal(t, task.EventClaimed, events[1].Type)
	require.Equal(t, task.EventFailed, events[2].Type)
	require.Equal(t, "boom", events[2].Message)

	attempts, err := s.Attempts(ctx, "task")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, task.StatusFailed, attempts[0].Status)
	require.Equal(t, "boom", attempts[0].Error)
	require.Equal(t, "2025-01-02T03:04:05Z", attempts[0].Started)
	require.Equal(t, "2025-01-02T03:04:05Z", events[1].At)
	require.Equal(t, "2025-01-02T03:05:05Z", attempts[0].Finished)
}

func TestSQLiteStoreIdempotentDoneDoesNotRecordDuplicateEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:00:00Z", Status: task.StatusTodo, Body: "body"}))
	_, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NoError(t, s.Update(ctx, "task", task.EventDone, "", func(tk *task.Task) bool {
		return tk.MarkDone(now)
	}))
	require.NoError(t, s.Update(ctx, "task", task.EventDone, "", func(tk *task.Task) bool {
		return tk.MarkDone(now)
	}))
	events, err := s.Events(ctx, "task")
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.Equal(t, task.EventDone, events[2].Type)
}

func TestSQLiteStoreSetDoingOpensRetryAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:00:00Z", Status: task.StatusTodo, Body: "body"}))
	_, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NoError(t, s.Update(ctx, "task", task.EventFailed, "boom", func(tk *task.Task) bool {
		return tk.MarkFailed(now.Add(time.Minute), "boom")
	}))
	require.NoError(t, s.Update(ctx, "task", task.EventClaimed, "retrying", func(tk *task.Task) bool {
		tk.MarkWorking(now.Add(2 * time.Minute))
		return true
	}))
	require.NoError(t, s.Update(ctx, "task", task.EventDone, "", func(tk *task.Task) bool {
		return tk.MarkDone(now.Add(3 * time.Minute))
	}))

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, tasks[0].Status)
	require.Empty(t, tasks[0].Error)

	attempts, err := s.Attempts(ctx, "task")
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.Equal(t, task.StatusFailed, attempts[0].Status)
	require.Equal(t, "boom", attempts[0].Error)
	require.Equal(t, task.StatusDone, attempts[1].Status)
	require.Empty(t, attempts[1].Error)
	require.Equal(t, "2025-01-02T03:06:05Z", attempts[1].Started)
	require.Equal(t, "2025-01-02T03:07:05Z", attempts[1].Finished)
}

func TestSQLiteStoreTerminalSetSynthesizesAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:00:00Z", Status: task.StatusTodo, Body: "body"}))
	require.NoError(t, s.Update(ctx, "task", task.EventDone, "", func(tk *task.Task) bool {
		return tk.MarkDone(now)
	}))

	attempts, err := s.Attempts(ctx, "task")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, task.StatusDone, attempts[0].Status)
	require.Equal(t, "2025-01-02T03:04:05Z", attempts[0].Started)
	require.Equal(t, "2025-01-02T03:04:05Z", attempts[0].Finished)
}

func TestSQLiteStoreRequeueStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 4, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "stale", Status: task.StatusTodo, Body: "stale"}))
	_, err := s.ClaimNext(ctx, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)

	requeued, err := s.RequeueStale(ctx, 30*time.Minute, now)
	require.NoError(t, err)
	require.Len(t, requeued, 1)
	require.Equal(t, "stale", requeued[0].ID)

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, tasks[0].Status)
	require.Empty(t, tasks[0].LeaseExpires)

	events, err := s.Events(ctx, "stale")
	require.NoError(t, err)
	require.Equal(t, task.EventRequeued, events[len(events)-1].Type)
}

func TestSQLiteStoreReapRecoversKilledWorkerLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 4, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "inflight", Status: task.StatusTodo, Body: "inflight"}))

	// Worker claims with a short lease that has already expired by `now`
	// (models a worker that died mid-execution and never heartbeated).
	claimed, err := s.ClaimNext(ctx, now.Add(-time.Hour), now.Add(-30*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, task.StatusDoing, claimed.Status)
	require.NotEmpty(t, claimed.LeaseExpires)

	// Reaper runs (cron tick). The lease is expired, so the task is requeued
	// regardless of the older-than fallback.
	requeued, err := s.RequeueStale(ctx, 20*time.Minute, now)
	require.NoError(t, err)
	require.Len(t, requeued, 1)
	require.Equal(t, "inflight", requeued[0].ID)

	// Task is back to todo with the lease cleared, and is claimable again.
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, tasks[0].Status)
	require.Empty(t, tasks[0].LeaseExpires)

	reclaimed, err := s.ClaimNext(ctx, now, now.Add(20*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	require.Equal(t, "inflight", reclaimed.ID)
	require.Equal(t, task.StatusDoing, reclaimed.Status)
}

func TestSQLiteStoreRequeueStaleSkipsRefreshedLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 4, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "inflight", Status: task.StatusTodo, Body: "inflight"}))
	_, err := s.ClaimNextForWorker(ctx, now.Add(-time.Hour), now.Add(-30*time.Minute), "worker-1", "codex")
	require.NoError(t, err)
	require.NoError(t, s.Heartbeat(ctx, "inflight", "worker-1", now, now.Add(30*time.Minute)))

	requeued, err := s.RequeueStale(ctx, 20*time.Minute, now)
	require.NoError(t, err)
	require.Empty(t, requeued)

	got, err := s.Get(ctx, "inflight")
	require.NoError(t, err)
	require.Equal(t, task.StatusDoing, got.Status)
	require.Equal(t, "2025-01-02T04:34:05Z", got.LeaseExpires)

	events, err := s.Events(ctx, "inflight")
	require.NoError(t, err)
	require.Equal(t, task.EventHeartbeat, events[len(events)-1].Type)
}

func TestSQLiteStoreRemoveAndPruneRecordEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "remove", Status: task.StatusTodo, Body: "remove"}))
	require.NoError(t, s.Delete(ctx, "remove"))
	events, err := s.Events(ctx, "remove")
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, task.EventRemoved, events[1].Type)

	require.NoError(t, s.Add(ctx, task.Task{ID: "prune", Status: task.StatusFailed, Body: "prune"}))
	pruned, err := s.Prune(ctx, []task.Status{task.StatusFailed})
	require.NoError(t, err)
	require.Equal(t, 1, pruned)
	events, err = s.Events(ctx, "prune")
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, task.EventPruned, events[1].Type)
	require.Equal(t, string(task.StatusFailed), events[1].Message)
}

func TestSQLiteStorePruneAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "keep", Status: task.StatusTodo}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "drop", Status: task.StatusFailed}))

	require.ErrorIs(t, s.Update(ctx, "missing", task.EventFailed, "", func(*task.Task) bool { return true }), store.ErrNotFound)
	require.ErrorIs(t, s.Delete(ctx, "missing"), store.ErrNotFound)
	require.True(t, errors.Is(store.ErrNotFound, store.ErrNotFound))

	pruned, err := s.Prune(ctx, []task.Status{task.StatusFailed})
	require.NoError(t, err)
	require.Equal(t, 1, pruned)
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "keep", tasks[0].ID)
}

func TestSQLiteStorePruneRejectsInvalidStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "keep", Status: task.StatusTodo}))
	pruned, err := s.Prune(ctx, []task.Status{"faield"})
	require.ErrorIs(t, err, task.ErrInvalidStatus)
	require.Equal(t, 0, pruned)

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
}

func TestPrune_RecordsPrunedEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	// Add tasks in terminal statuses and one pending (must survive).
	require.NoError(t, s.Add(ctx, task.Task{ID: "d1", Status: task.StatusDone, Body: "done one"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "d2", Status: task.StatusDone, Body: "done two"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "f1", Status: task.StatusFailed, Body: "fail one"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "keep", Status: task.StatusTodo, Body: "keep"}))

	pruned, err := s.Prune(ctx, []task.Status{task.StatusDone, task.StatusFailed})
	require.NoError(t, err)
	require.Equal(t, 3, pruned)

	// Surviving task is untouched.
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "keep", tasks[0].ID)

	// Each pruned task must have a Pruned event whose message is the status string.
	for id, wantStatus := range map[string]task.Status{
		"d1": task.StatusDone,
		"d2": task.StatusDone,
		"f1": task.StatusFailed,
	} {
		events, err := s.Events(ctx, id)
		require.NoError(t, err, "events for %s", id)
		require.True(t, len(events) >= 2, "expected at least 2 events for %s, got %d", id, len(events))
		last := events[len(events)-1]
		require.Equal(t, task.EventPruned, last.Type, "last event type for %s", id)
		require.Equal(t, string(wantStatus), last.Message, "last event message for %s", id)
	}
}

func TestSQLiteStoreMigratesOldSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "old.sqlite")
	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: dbPath})
	require.NoError(t, err)
	require.NoError(t, s.Close())

	// Opening a DB with the current schema a second time exercises idempotent ALTER migrations.
	s, err = store.NewSQLite(ctx, store.Paths{SQLitePath: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusTodo, Body: "ok", Tags: []string{"x"}}))
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"x"}, tasks[0].Tags)
}

func TestSQLiteStoreCreatesParentDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "missing", "tasks.sqlite")

	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusTodo}))
	require.FileExists(t, dbPath)
}

func TestResolvePaths(t *testing.T) {
	t.Parallel()

	// A stale `.jsonl` path normalizes to a sibling `.sqlite` database.
	paths := store.ResolvePaths("/tmp/tasks.jsonl")
	require.Equal(t, "/tmp/tasks.sqlite", paths.SQLitePath)

	// A `.sqlite` path is used as-is.
	paths = store.ResolvePaths("/tmp/tasks.sqlite")
	require.Equal(t, "/tmp/tasks.sqlite", paths.SQLitePath)

	// An extensionless path is used as-is.
	paths = store.ResolvePaths("/tmp/tasks")
	require.Equal(t, "/tmp/tasks", paths.SQLitePath)

	// Any other extension normalizes to a sibling `.sqlite` database.
	paths = store.ResolvePaths("/tmp/tasks.db")
	require.Equal(t, "/tmp/tasks.sqlite", paths.SQLitePath)

	// An empty path falls back to the default SQLite location.
	defaultPath, err := store.DefaultPath()
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(defaultPath, ".claude/queue/tasks.sqlite"))
	require.Equal(t, defaultPath, store.ResolvePaths("").SQLitePath)
}

func TestDelete_RecordsRemovedEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "del-1", Status: task.StatusTodo, Body: "to delete"}))
	require.NoError(t, s.Delete(ctx, "del-1"))

	events, err := s.Events(ctx, "del-1")
	require.NoError(t, err)
	var found bool
	for _, e := range events {
		if e.Type == task.EventRemoved {
			found = true
			break
		}
	}
	require.True(t, found, "expected EventRemoved in events after Delete")
}

func requireIDs(t *testing.T, tasks []task.Task, ids ...string) {
	t.Helper()
	got := make([]string, 0, len(tasks))
	for _, tk := range tasks {
		got = append(got, tk.ID)
	}
	require.Equal(t, ids, got)
}

func TestFind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusTodo, Body: "fix search matching", Tags: []string{"area:search"}}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "2", Status: task.StatusTodo, Body: "unrelated visible task", CWD: "/tmp/beta"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "3", Status: task.StatusTodo, Body: "deleted search match", ResourceKey: "repo:/tmp/gamma"}))

	all, err := s.Find(ctx, "   ")
	require.NoError(t, err)
	require.Len(t, all, 3)

	bySearch, err := s.Find(ctx, "search")
	require.NoError(t, err)
	require.Len(t, bySearch, 2)
	require.Equal(t, "1", bySearch[0].ID)
	require.Equal(t, "3", bySearch[1].ID)

	byTag, err := s.Find(ctx, "area:search")
	require.NoError(t, err)
	require.Len(t, byTag, 1)
	require.Equal(t, "1", byTag[0].ID)

	byResource, err := s.Find(ctx, "gamma")
	require.NoError(t, err)
	require.Len(t, byResource, 1)
	require.Equal(t, "3", byResource[0].ID)

	// AU trigger: updating body must re-index.
	require.NoError(t, s.Update(ctx, "2", task.EventFailed, "", func(tk *task.Task) bool {
		tk.Body = "now mentions search too"
		return true
	}))
	afterUpdate, err := s.Find(ctx, "search")
	require.NoError(t, err)
	require.Len(t, afterUpdate, 3)

	// AU trigger WHEN clause: a lease-only update (no FTS-indexed column
	// changes) must NOT rebuild the FTS row — task 3 stays findable by its
	// original body with content unchanged.
	require.NoError(t, s.Update(ctx, "3", task.EventHeartbeat, "", func(tk *task.Task) bool {
		tk.LeaseExpires = time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
		return true
	}))
	afterLeaseBump, err := s.Find(ctx, "search")
	require.NoError(t, err)
	require.Len(t, afterLeaseBump, 3)
	stillByBody, err := s.Find(ctx, "deleted")
	require.NoError(t, err)
	require.Len(t, stillByBody, 1)
	require.Equal(t, "3", stillByBody[0].ID)

	// AD trigger: deleting a task must drop it from the index.
	require.NoError(t, s.Delete(ctx, "1"))
	afterDelete, err := s.Find(ctx, "search")
	require.NoError(t, err)
	require.Len(t, afterDelete, 2)
	for _, tk := range afterDelete {
		require.NotEqual(t, "1", tk.ID)
	}

	// Arbitrary user text with FTS operators must not error.
	none, err := s.Find(ctx, `"(NOT no-such-token)"`)
	require.NoError(t, err)
	require.Empty(t, none)
}

func BenchmarkFind(b *testing.B) {
	ctx := context.Background()
	s, err := store.NewSQLite(ctx, store.Paths{
		SQLitePath: filepath.Join(b.TempDir(), "tasks.sqlite"),
	})
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, s.Close()) })

	const n = 10000
	for i := 0; i < n; i++ {
		body := "routine task body number"
		if i%500 == 0 {
			body = "special needle marker body"
		}
		require.NoError(b, s.Add(ctx, task.Task{
			ID:     fmt.Sprintf("task-%05d", i),
			Status: task.StatusTodo,
			Body:   body,
			Tags:   []string{fmt.Sprintf("batch:%d", i%10)},
		}))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := s.Find(ctx, "needle")
		if err != nil {
			b.Fatal(err)
		}
		if len(out) == 0 {
			b.Fatal("expected matches")
		}
	}
}

func TestRecentDistinctCWDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	tasks := []task.Task{
		{ID: "t1", Status: task.StatusTodo, Body: "old", CWD: "/old", Created: "2025-01-02T03:00:00Z"},
		{ID: "t2", Status: task.StatusTodo, Body: "new-b-1", CWD: "/new-b", Created: "2025-01-02T03:04:00Z"},
		{ID: "t3", Status: task.StatusTodo, Body: "new-a", CWD: "/new-a", Created: "2025-01-02T03:03:00Z"},
		{ID: "t4", Status: task.StatusTodo, Body: "new-b-2", CWD: "/new-b", Created: "2025-01-02T03:02:00Z"},
		{ID: "t5", Status: task.StatusTodo, Body: "empty-cwd", CWD: "", Created: "2025-01-02T03:05:00Z"},
	}
	for _, tk := range tasks {
		require.NoError(t, s.Add(ctx, tk))
	}

	t.Run("limit=2 returns two most-recent distinct cwds alphabetically", func(t *testing.T) {
		t.Parallel()
		got, err := s.RecentDistinctCWDs(ctx, 2)
		require.NoError(t, err)
		require.Equal(t, []string{"/new-a", "/new-b"}, got)
	})

	t.Run("limit=0 returns all distinct non-empty cwds alphabetically", func(t *testing.T) {
		t.Parallel()
		got, err := s.RecentDistinctCWDs(ctx, 0)
		require.NoError(t, err)
		require.Equal(t, []string{"/new-a", "/new-b", "/old"}, got)
	})
}
