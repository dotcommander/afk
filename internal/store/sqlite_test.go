package store_test

import (
	"context"
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusPending, Body: "one"}))
	require.ErrorIs(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusPending, Body: "dupe"}), store.ErrDuplicateTask)
	require.NoError(t, s.Update(ctx, "1", task.EventEdited, "", func(tk *task.Task) bool {
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

func TestSQLiteStoreNewSQLiteReportsInvalidParentPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(filePath, nil, 0o600))

	_, err := store.NewSQLite(ctx, store.Paths{SQLitePath: filepath.Join(filePath, "tasks.sqlite")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mkdir sqlite dir")
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
			errs <- s.Add(ctx, task.Task{ID: id, Status: task.StatusPending, Body: id})
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
		Status:      task.StatusPending,
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
	require.Equal(t, "high", tasks[0].Priority)
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
	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusPending, Body: "pending"}))

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "pending", claimed.ID)
	require.Equal(t, task.StatusWorking, claimed.Status)
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
	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusPending, Body: "first"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "failed", Status: task.StatusFailed, Body: "failed"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusPending, Body: "second"}))

	ready, err := s.Ready(ctx, store.ReadyOptions{})
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
		{ID: "normal", Status: task.StatusPending, Body: "normal"},
		{ID: "low", Status: task.StatusPending, Body: "low", Priority: "low"},
		{ID: "unknown", Status: task.StatusPending, Body: "unknown", Priority: "later"},
		{ID: "urgent", Status: task.StatusPending, Body: "urgent", Priority: " urgent "},
		{ID: "high", Status: task.StatusPending, Body: "high", Priority: "HIGH"},
	} {
		require.NoError(t, s.Add(ctx, tsk))
	}

	ready, err := s.Ready(ctx, store.ReadyOptions{})
	require.NoError(t, err)
	requireIDs(t, ready, "urgent", "high", "normal", "unknown", "low")
}

func TestSQLiteStoreClaimNextOrdersByPriority(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "normal", Status: task.StatusPending, Body: "normal"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "urgent", Status: task.StatusPending, Body: "urgent", Priority: "urgent"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "high", Status: task.StatusPending, Body: "high", Priority: "high"}))

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
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked-by-dep", Status: task.StatusPending, Body: "blocked", Priority: "urgent"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusPending, Body: "prereq"}))
	require.NoError(t, s.AddDependency(ctx, "blocked-by-dep", "prereq"))

	require.NoError(t, s.Add(ctx, task.Task{ID: "manual", Status: task.StatusPending, Body: "manual", Priority: "urgent"}))
	require.NoError(t, s.Block(ctx, "manual", "waiting"))

	require.NoError(t, s.Add(ctx, task.Task{ID: "resource-active", Status: task.StatusWorking, Body: "active", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "resource-blocked", Status: task.StatusPending, Body: "blocked", Priority: "urgent", ResourceKey: "repo:x"}))

	ready, err := s.Ready(ctx, store.ReadyOptions{Now: now.Add(time.Second)})
	require.NoError(t, err)
	requireIDs(t, ready, "prereq")
}

func TestSQLiteStoreReadyAgreesWithClaimNext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "done", Status: task.StatusDone, Body: "done"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusPending, Body: "first"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusPending, Body: "second"}))

	ready, err := s.Ready(ctx, store.ReadyOptions{Now: now})
	require.NoError(t, err)
	require.Len(t, ready, 2)

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, ready[0].ID, claimed.ID)
}

func TestSQLiteStorePromote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "urgent", Status: task.StatusPending, Body: "urgent", Priority: "urgent"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusPending, Body: "first"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusPending, Body: "second"}))

	require.NoError(t, s.Promote(ctx, "second"))

	ready, err := s.Ready(ctx, store.ReadyOptions{})
	require.NoError(t, err)
	requireIDs(t, ready, "urgent", "second", "first")

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	requireIDs(t, tasks, "second", "urgent", "first")

	events, err := s.Events(ctx, "second")
	require.NoError(t, err)
	require.Equal(t, task.EventPromoted, events[len(events)-1].Type)
}

func TestSQLiteStorePromoteRejectsNonPendingTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "working", Status: task.StatusWorking, Body: "working"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "done", Status: task.StatusDone, Body: "done"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "failed", Status: task.StatusFailed, Body: "failed"}))

	require.ErrorIs(t, s.Promote(ctx, "missing"), store.ErrNotFound)
	require.ErrorIs(t, s.Promote(ctx, "working"), store.ErrInvalidState)
	require.ErrorIs(t, s.Promote(ctx, "done"), store.ErrInvalidState)
	require.ErrorIs(t, s.Promote(ctx, "failed"), store.ErrInvalidState)
}

func TestSQLiteStoreReadyExcludesUnfinishedDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusPending, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusPending, Body: "prereq"}))
	require.NoError(t, s.AddDependency(ctx, "blocked", "prereq"))

	ready, err := s.Ready(ctx, store.ReadyOptions{})
	require.NoError(t, err)
	require.Len(t, ready, 1)
	require.Equal(t, "prereq", ready[0].ID)
}

func TestSQLiteStoreReadyExcludesMissingDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusPending, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusPending, Body: "prereq"}))
	require.NoError(t, s.AddDependency(ctx, "blocked", "prereq"))
	require.NoError(t, s.Delete(ctx, "prereq"))

	ready, err := s.Ready(ctx, store.ReadyOptions{Now: now})
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusPending, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusPending, Body: "prereq"}))

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

	require.NoError(t, s.RemoveDependency(ctx, "blocked", "prereq"))
	deps, err = s.Dependencies(ctx, "blocked")
	require.NoError(t, err)
	require.Empty(t, deps)

	events, err = s.Events(ctx, "blocked")
	require.NoError(t, err)
	require.Equal(t, task.EventDependencyRemoved, events[len(events)-1].Type)
	require.Equal(t, "prereq", events[len(events)-1].Message)
}

func TestSQLiteStoreDependencyValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "a", Status: task.StatusPending, Body: "a"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "b", Status: task.StatusPending, Body: "b"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "c", Status: task.StatusPending, Body: "c"}))

	require.ErrorIs(t, s.AddDependency(ctx, "a", "a"), store.ErrInvalidDependency)
	require.ErrorIs(t, s.AddDependency(ctx, "a", "missing"), store.ErrNotFound)
	require.ErrorIs(t, s.AddDependency(ctx, "missing", "a"), store.ErrNotFound)

	require.NoError(t, s.AddDependency(ctx, "b", "a"))
	require.NoError(t, s.AddDependency(ctx, "c", "b"))
	require.ErrorIs(t, s.AddDependency(ctx, "a", "c"), store.ErrDependencyCycle)
}

func TestSQLiteStoreBulkAddTasksAndDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	tasks := []task.Task{
		{ID: "prereq", Created: "2025-01-02T03:04:05Z", Status: task.StatusPending, Body: "prereq", Tags: []string{"spec:bulk"}},
		{ID: "blocked", Created: "2025-01-02T03:04:05Z", Status: task.StatusPending, Body: "blocked"},
	}
	deps := []task.Dependency{{
		TaskID:      "blocked",
		DependsOnID: "prereq",
		Created:     "2025-01-02T03:04:05Z",
	}}
	require.NoError(t, s.BulkAdd(ctx, tasks, deps))

	got, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "prereq", got[0].ID)
	require.Equal(t, []string{"spec:bulk"}, got[0].Tags)

	gotDeps, err := s.Dependencies(ctx, "blocked")
	require.NoError(t, err)
	require.Equal(t, deps, gotDeps)

	events, err := s.Events(ctx, "blocked")
	require.NoError(t, err)
	require.Equal(t, task.EventDependencyAdded, events[len(events)-1].Type)
	eventCount := len(events)

	require.NoError(t, s.BulkAdd(ctx, nil, deps))
	events, err = s.Events(ctx, "blocked")
	require.NoError(t, err)
	require.Len(t, events, eventCount)
}

func TestSQLiteStoreBulkAddValidatesDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.BulkAdd(ctx, nil, nil))
	require.ErrorIs(t, s.BulkAdd(ctx, []task.Task{
		{ID: "self", Status: task.StatusPending, Body: "self"},
	}, []task.Dependency{{TaskID: "self", DependsOnID: "self"}}), store.ErrInvalidDependency)

	require.NoError(t, s.BulkAdd(ctx, []task.Task{
		{ID: "a", Status: task.StatusPending, Body: "a"},
		{ID: "b", Status: task.StatusPending, Body: "b"},
		{ID: "c", Status: task.StatusPending, Body: "c"},
	}, nil))
	require.NoError(t, s.AddDependency(ctx, "b", "a"))
	require.NoError(t, s.AddDependency(ctx, "c", "b"))
	require.ErrorIs(t, s.BulkAdd(ctx, nil, []task.Dependency{
		{TaskID: "a", DependsOnID: "c"},
	}), store.ErrDependencyCycle)
}

func TestSQLiteStoreDependenciesAffectClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusPending, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusPending, Body: "prereq"}))
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

func TestSQLiteStoreManualBlocks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "blocked", Status: task.StatusPending, Body: "blocked"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "next", Status: task.StatusPending, Body: "next"}))
	require.NoError(t, s.Block(ctx, "blocked", "waiting on credentials"))

	block, err := s.BlockForTask(ctx, "blocked")
	require.NoError(t, err)
	require.NotNil(t, block)
	require.Equal(t, "waiting on credentials", block.Reason)

	ready, err := s.Ready(ctx, store.ReadyOptions{Now: now})
	require.NoError(t, err)
	require.Len(t, ready, 1)
	require.Equal(t, "next", ready[0].ID)

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "next", claimed.ID)

	require.NoError(t, s.Unblock(ctx, "blocked"))
	block, err = s.BlockForTask(ctx, "blocked")
	require.NoError(t, err)
	require.Nil(t, block)
	require.ErrorIs(t, s.Unblock(ctx, "blocked"), store.ErrBlockNotFound)

	ready, err = s.Ready(ctx, store.ReadyOptions{Now: now})
	require.NoError(t, err)
	require.Len(t, ready, 1)
	require.Equal(t, "blocked", ready[0].ID)

	events, err := s.Events(ctx, "blocked")
	require.NoError(t, err)
	require.Equal(t, task.EventUnblocked, events[len(events)-1].Type)
}

func TestSQLiteStoreResourceLocksAffectClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusPending, Body: "first", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusPending, Body: "second", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "other", Status: task.StatusPending, Body: "other", ResourceKey: "repo:y"}))

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "first", claimed.ID)

	ready, err := s.Ready(ctx, store.ReadyOptions{Now: now})
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "first", Status: task.StatusPending, Body: "first", ResourceKey: "repo:x"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "second", Status: task.StatusPending, Body: "second", ResourceKey: "repo:x"}))
	_, err := s.ClaimNext(ctx, now.Add(-time.Hour), now.Add(-30*time.Minute))
	require.NoError(t, err)

	ready, err := s.Ready(ctx, store.ReadyOptions{Now: now})
	require.NoError(t, err)
	require.Empty(t, ready)

	claimed, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.Nil(t, claimed)

	_, err = s.RequeueStale(ctx, time.Minute, now)
	require.NoError(t, err)

	ready, err = s.Ready(ctx, store.ReadyOptions{Now: now})
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusPending, Body: "pending"}))
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusPending, Body: "pending"}))
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusPending, Body: "pending"}))
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "pending", Status: task.StatusPending, Body: "pending"}))
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
		require.NoError(t, s.Add(ctx, task.Task{ID: id, Status: task.StatusPending, Body: id}))
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
		Status:  task.StatusPending,
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:00:00Z", Status: task.StatusPending, Body: "body"}))
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

func TestSQLiteStoreResetClosesActiveAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "task", Created: "2025-01-02T03:00:00Z", Status: task.StatusPending, Body: "body"}))
	_, err := s.ClaimNext(ctx, now, time.Time{})
	require.NoError(t, err)
	require.NoError(t, s.Update(ctx, "task", task.EventReset, "", func(tk *task.Task) bool {
		tk.Reset()
		return true
	}))

	attempts, err := s.Attempts(ctx, "task")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, task.StatusPending, attempts[0].Status)
	require.NotEmpty(t, attempts[0].Finished)

	events, err := s.Events(ctx, "task")
	require.NoError(t, err)
	require.Equal(t, task.EventReset, events[len(events)-1].Type)
}

func TestSQLiteStoreRequeueStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2025, 1, 2, 4, 4, 5, 0, time.UTC)

	require.NoError(t, s.Add(ctx, task.Task{ID: "stale", Status: task.StatusPending, Body: "stale"}))
	_, err := s.ClaimNext(ctx, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)

	requeued, err := s.RequeueStale(ctx, 30*time.Minute, now)
	require.NoError(t, err)
	require.Len(t, requeued, 1)
	require.Equal(t, "stale", requeued[0].ID)

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, task.StatusPending, tasks[0].Status)
	require.Empty(t, tasks[0].LeaseExpires)

	events, err := s.Events(ctx, "stale")
	require.NoError(t, err)
	require.Equal(t, task.EventRequeued, events[len(events)-1].Type)
}

func TestSQLiteStoreRemoveAndPruneRecordEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "remove", Status: task.StatusPending, Body: "remove"}))
	require.NoError(t, s.Delete(ctx, "remove"))
	events, err := s.Events(ctx, "remove")
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, task.EventRemoved, events[1].Type)

	require.NoError(t, s.Add(ctx, task.Task{ID: "prune", Status: task.StatusFailed, Body: "prune"}))
	require.NoError(t, s.Prune(ctx, []task.Status{task.StatusFailed}))
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "keep", Status: task.StatusPending}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "drop", Status: task.StatusFailed}))

	require.ErrorIs(t, s.Update(ctx, "missing", task.EventEdited, "", func(*task.Task) bool { return true }), store.ErrNotFound)
	require.ErrorIs(t, s.Delete(ctx, "missing"), store.ErrNotFound)
	require.ErrorIs(t, s.RemoveDependency(ctx, "keep", "missing"), store.ErrDependencyNotFound)
	require.True(t, errors.Is(store.ErrNotFound, store.ErrNotFound))

	require.NoError(t, s.Prune(ctx, []task.Status{task.StatusFailed}))
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "keep", tasks[0].ID)
}

func TestPrune_RecordsPrunedEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	// Add tasks in terminal statuses and one pending (must survive).
	require.NoError(t, s.Add(ctx, task.Task{ID: "d1", Status: task.StatusDone, Body: "done one"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "d2", Status: task.StatusDone, Body: "done two"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "f1", Status: task.StatusFailed, Body: "fail one"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "keep", Status: task.StatusPending, Body: "keep"}))

	require.NoError(t, s.Prune(ctx, []task.Status{task.StatusDone, task.StatusFailed}))

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

func TestSQLiteStorePruneByTag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dir := t.TempDir()
	s, err := store.NewSQLite(ctx, store.Paths{
		SQLitePath: filepath.Join(dir, "tasks.sqlite"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.NoError(t, s.Add(ctx, task.Task{ID: "alpha1", Status: task.StatusPending, Tags: []string{"spec:alpha"}}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "alpha2", Status: task.StatusPending, Tags: []string{"spec:alpha"}}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "beta1", Status: task.StatusPending, Tags: []string{"spec:beta"}}))

	n, err := s.PruneByTag(ctx, "spec:alpha")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	tasks, err := s.List(ctx)
	require.NoError(t, err)
	ids := make([]string, 0, len(tasks))
	for _, tk := range tasks {
		ids = append(ids, tk.ID)
	}
	require.NotContains(t, ids, "alpha1")
	require.NotContains(t, ids, "alpha2")
	require.Contains(t, ids, "beta1")

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		n2, err2 := s.PruneByTag(ctx, "spec:nomatch")
		require.NoError(t, err2)
		require.Equal(t, 0, n2)
	})

	t.Run("empty tag", func(t *testing.T) {
		t.Parallel()
		_, err3 := s.PruneByTag(ctx, "")
		require.Error(t, err3)
		require.Contains(t, err3.Error(), "must not be empty")
	})
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
	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusPending, Body: "ok", Tags: []string{"x"}}))
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusPending}))
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

	require.NoError(t, s.Add(ctx, task.Task{ID: "del-1", Status: task.StatusPending, Body: "to delete"}))
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
