package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/queue"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.NewSQLite(context.Background(), store.Paths{
		SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite"),
		JSONLPath:  filepath.Join(t.TempDir(), "tasks.jsonl"),
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

func TestSQLiteStoreClaimNextEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	claimed, err := s.ClaimNext(ctx, time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), time.Time{})
	require.NoError(t, err)
	require.Nil(t, claimed)
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
	require.True(t, errors.Is(store.ErrNotFound, store.ErrNotFound))

	require.NoError(t, s.Prune(ctx, []task.Status{task.StatusFailed}))
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "keep", tasks[0].ID)
}

func TestSQLiteStoreImportsLegacyJSONLOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "tasks.jsonl")
	sqlitePath := filepath.Join(dir, "tasks.sqlite")
	legacy := queue.New(jsonlPath)
	require.NoError(t, legacy.Save(ctx, []task.Task{
		{ID: "a", Created: "2025-01-01T00:00:00Z", Status: task.StatusPending, Body: "first"},
		{ID: "b", Created: "2025-01-02T00:00:00Z", Status: task.StatusDone, Body: "second"},
	}))

	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: sqlitePath, JSONLPath: jsonlPath})
	require.NoError(t, err)
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, "a", tasks[0].ID)
	require.NoError(t, s.Close())

	require.NoError(t, legacy.Save(ctx, []task.Task{
		{ID: "c", Created: "2025-01-03T00:00:00Z", Status: task.StatusPending, Body: "third"},
	}))
	s, err = store.NewSQLite(ctx, store.Paths{SQLitePath: sqlitePath, JSONLPath: jsonlPath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	tasks, err = s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
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

func TestSQLiteStoreDoesNotImportJSONLIntoNonEmptyDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "tasks.jsonl")
	sqlitePath := filepath.Join(dir, "tasks.sqlite")

	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: sqlitePath, JSONLPath: ""})
	require.NoError(t, err)
	require.NoError(t, s.Add(ctx, task.Task{ID: "sqlite", Status: task.StatusPending, Body: "db"}))
	require.NoError(t, s.Close())

	require.NoError(t, queue.New(jsonlPath).Save(ctx, []task.Task{
		{ID: "jsonl", Created: "2025-01-01T00:00:00Z", Status: task.StatusPending, Body: "jsonl"},
	}))
	s, err = store.NewSQLite(ctx, store.Paths{SQLitePath: sqlitePath, JSONLPath: jsonlPath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "sqlite", tasks[0].ID)
}

func TestSQLiteStoreMissingJSONLDoesNotMarkImported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "tasks.jsonl")
	sqlitePath := filepath.Join(dir, "tasks.sqlite")

	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: sqlitePath, JSONLPath: jsonlPath})
	require.NoError(t, err)
	require.NoError(t, s.Close())

	require.NoError(t, queue.New(jsonlPath).Save(ctx, []task.Task{
		{ID: "late", Created: "2025-01-01T00:00:00Z", Status: task.StatusPending, Body: "late"},
	}))
	s, err = store.NewSQLite(ctx, store.Paths{SQLitePath: sqlitePath, JSONLPath: jsonlPath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	tasks, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "late", tasks[0].ID)
}

func TestSQLiteStoreCreatesParentDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "missing", "tasks.sqlite")

	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.NoFileExists(t, filepath.Join(filepath.Dir(dbPath), "tasks.jsonl"))

	require.NoError(t, s.Add(ctx, task.Task{ID: "1", Status: task.StatusPending}))
	require.FileExists(t, dbPath)
}

func TestResolvePaths(t *testing.T) {
	t.Parallel()

	paths := store.ResolvePaths("/tmp/tasks.jsonl")
	require.Equal(t, "/tmp/tasks.sqlite", paths.SQLitePath)
	require.Equal(t, "/tmp/tasks.jsonl", paths.JSONLPath)

	paths = store.ResolvePaths("/tmp/tasks.sqlite")
	require.Equal(t, "/tmp/tasks.sqlite", paths.SQLitePath)
	require.Equal(t, "/tmp/tasks.jsonl", paths.JSONLPath)

	paths = store.ResolvePaths("/tmp/tasks")
	require.Equal(t, "/tmp/tasks", paths.SQLitePath)
	require.Equal(t, "/tmp/tasks.jsonl", paths.JSONLPath)

	defaultPath, err := store.DefaultPath()
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(defaultPath, ".claude/queue/tasks.sqlite"))
}
