package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

var fixed = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

func newService(t *testing.T) *app.Service {
	t.Helper()
	return newServiceWithNow(t, func() time.Time { return fixed })
}

func newServiceWithNow(t *testing.T, now func() time.Time) *app.Service {
	t.Helper()
	s, err := store.NewSQLite(context.Background(), store.Paths{
		SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	s.SetClock(now)
	return app.NewService(s, now)
}

func newServiceWithSidecar(t *testing.T) (*app.Service, string) {
	t.Helper()
	dir := t.TempDir()
	sidecar := filepath.Join(dir, "rejected.jsonl")
	s, err := store.NewSQLite(context.Background(), store.Paths{
		SQLitePath: filepath.Join(dir, "tasks.sqlite"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	svc := app.NewService(s, func() time.Time { return fixed }, app.WithSidecarPath(sidecar))
	return svc, sidecar
}

func validDiscoveryBody(path string) string {
	return "[discovery:repo:file] Fix the focused issue. Evidence: " + path + ":1. Scope: " + path + ". Success: focused issue is fixed. Verify with go test ./... Reject-if: evidence no longer matches."
}

func TestServiceLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "hello")
	require.NoError(t, err)
	require.NotEmpty(t, id)

	tasks, err := svc.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, task.StatusTodo, tasks[0].Status)

	next, err := svc.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, id, next.ID)

	popped, err := svc.Take(ctx, 0, "lifecycle-worker", "")
	require.NoError(t, err)
	require.NotNil(t, popped)
	require.Equal(t, task.StatusDoing, popped.Status)
	require.Equal(t, fixed.Format(time.RFC3339), popped.Started)

	require.NoError(t, svc.SetStatusWithStageWorker(ctx, id, task.StatusDone, "verified", nil, "lifecycle-worker"))
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)

	id, err = svc.Add(ctx, "fail me")
	require.NoError(t, err)
	require.NoError(t, svc.Fail(ctx, id, "oops"))
	tally, err := svc.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, tally[task.StatusFailed])

	pruned, err := svc.Prune(ctx, []task.Status{task.StatusDone, task.StatusFailed})
	require.NoError(t, err)
	require.Equal(t, 2, pruned)
	tasks, err = svc.List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, tasks)
}

func TestServiceSetStatusRequiresTerminalNoteUnlessForced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	doneID, err := svc.Add(ctx, "terminal note task")
	require.NoError(t, err)
	err = svc.SetStatus(ctx, doneID, task.StatusDone, "")
	require.ErrorIs(t, err, task.ErrMissingCompletionNote)
	got, err := svc.Show(ctx, doneID)
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, got.Status)

	require.NoError(t, svc.SetStatusWithStageForce(ctx, doneID, task.StatusDone, "", nil))
	got, err = svc.Show(ctx, doneID)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)

	failedID, err := svc.Add(ctx, "terminal fail task")
	require.NoError(t, err)
	err = svc.SetStatus(ctx, failedID, task.StatusFailed, "   ")
	require.ErrorIs(t, err, task.ErrMissingCompletionNote)
	require.NoError(t, svc.SetStatus(ctx, failedID, task.StatusFailed, "verified failure"))
}

func TestServiceFilteringAndCollisionIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	first, err := svc.Add(ctx, "first")
	require.NoError(t, err)
	second, err := svc.Add(ctx, "second")
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	pending, err := svc.List(ctx, string(task.StatusTodo))
	require.NoError(t, err)
	require.Len(t, pending, 2)
	require.Equal(t, "second", pending[1].Body)

	done, err := svc.List(ctx, string(task.StatusDone))
	require.NoError(t, err)
	require.Empty(t, done)
}

func TestServiceFindAndRecentPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	first, err := svc.AddWithOptions(ctx, task.AddOptions{
		Body:        "fix search matching",
		CWD:         "/tmp/alpha",
		ResourceKey: "repo:/tmp/alpha",
		Tags:        []string{"area:search"},
	})
	require.NoError(t, err)
	second, err := svc.AddWithOptions(ctx, task.AddOptions{
		Body: "unrelated visible task",
		CWD:  "/tmp/beta",
	})
	require.NoError(t, err)
	deleted, err := svc.AddWithOptions(ctx, task.AddOptions{
		Body: "deleted search match",
		CWD:  "/tmp/deleted",
	})
	require.NoError(t, err)
	require.NoError(t, svc.SetStatus(ctx, deleted, task.StatusDeleted, "obsolete"))

	found, err := svc.Find(ctx, "area:search", "")
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, first, found[0].ID)

	found, err = svc.Find(ctx, "search", "all")
	require.NoError(t, err)
	require.Len(t, found, 2)

	found, err = svc.Find(ctx, "visible", "todo")
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, second, found[0].ID)

	_, err = svc.Find(ctx, "anything", "not-real")
	require.ErrorIs(t, err, task.ErrInvalidStatus)

	paths, err := svc.RecentPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/tmp/alpha", "/tmp/beta", "/tmp/deleted"}, paths)
}

func TestServiceStatusSnapshotUsesCanonicalTodoDoing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	todoID, err := svc.Add(ctx, "todo task")
	require.NoError(t, err)
	doingID, err := svc.Add(ctx, "doing task")
	require.NoError(t, err)
	doneID, err := svc.Add(ctx, "done task")
	require.NoError(t, err)
	deletedID, err := svc.Add(ctx, "deleted task")
	require.NoError(t, err)

	claimed, err := svc.Take(ctx, 0, "worker-1", "")
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, todoID, claimed.ID)
	require.NoError(t, svc.SetStatus(ctx, doneID, task.StatusDone, "verified"))
	require.NoError(t, svc.SetStatus(ctx, deletedID, task.StatusDeleted, "obsolete"))

	snapshot, err := svc.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Counts[task.StatusTodo])
	require.Equal(t, 1, snapshot.Counts[task.StatusDoing])
	require.Equal(t, 1, snapshot.Counts[task.StatusDone])
	require.Equal(t, 1, snapshot.Counts[task.StatusDeleted])
	require.Len(t, snapshot.Todo, 1)
	require.Equal(t, doingID, snapshot.Todo[0].ID)
	require.Len(t, snapshot.Doing, 1)
	require.Equal(t, todoID, snapshot.Doing[0].ID)
	require.Equal(t, int64(86400), snapshot.Health.WindowSeconds)
	require.NotNil(t, snapshot.Health.OldestReadyAgeSeconds)
	require.NotNil(t, snapshot.Health.OldestActiveAgeSeconds)
}

// Duplicate-ID retry tests were removed when addValidated switched to UUID-
// based IDs (uuid.NewString). UUIDv4 collisions in this universe are not a
// realistic concern, so no List/retry loop exists to verify.

func TestServiceRejectsInvalidTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t)

	_, err := svc.Add(ctx, "pick my nose")
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	_, err = svc.AddWithOptions(ctx, task.AddOptions{
		Body:   "Fix /tmp/repo/file.go. Verify with go test ./...",
		Source: "task-discovery",
		CWD:    "/tmp/repo",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	id, err := svc.Add(ctx, "valid software task")
	require.NoError(t, err)
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "valid software task", got.Body)
}

func TestServiceAddWithOptionsStoresMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.AddWithOptions(ctx, task.AddOptions{
		Body:        "metadata",
		Priority:    "high",
		Tags:        []string{"repo:afk", "type:test"},
		CWD:         "/tmp/repo",
		Source:      "cli",
		Agent:       "codex",
		GroupID:     "group",
		ResourceKey: "repo:/tmp/repo",
	})
	require.NoError(t, err)

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.PriorityHigh, got.Priority)
	require.Equal(t, []string{"repo:afk", "type:test"}, got.Tags)
	require.Equal(t, "/tmp/repo", got.CWD)
	require.Equal(t, "cli", got.Source)
	require.Equal(t, "codex", got.Agent)
	require.Equal(t, "group", got.GroupID)
	require.Equal(t, "repo:/tmp/repo", got.ResourceKey)
}

func TestServiceDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	blocked, err := svc.Add(ctx, "blocked")
	require.NoError(t, err)
	prereq, err := svc.Add(ctx, "prereq")
	require.NoError(t, err)

	require.NoError(t, svc.AddDependency(ctx, blocked, prereq))
	deps, err := svc.Dependencies(ctx, blocked)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, blocked, deps[0].TaskID)
	require.Equal(t, prereq, deps[0].DependsOnID)

	deps, err = svc.Dependencies(ctx, blocked)
	require.NoError(t, err)
	require.Len(t, deps, 1)
}

func TestServiceReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	blocked, err := svc.Add(ctx, "blocked")
	require.NoError(t, err)
	prereq, err := svc.Add(ctx, "prereq")
	require.NoError(t, err)
	independent, err := svc.Add(ctx, "independent")
	require.NoError(t, err)
	require.NoError(t, svc.AddDependency(ctx, blocked, prereq))

	ready, err := svc.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 2)
	require.Equal(t, prereq, ready[0].ID)
	require.Equal(t, independent, ready[1].ID)

	require.NoError(t, svc.Done(ctx, prereq, "prereq verified"))
	ready, err = svc.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 2)
	require.Equal(t, blocked, ready[0].ID)
	require.Equal(t, independent, ready[1].ID)
}

func TestServiceReadyExcludesFailedDependency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	blocked, err := svc.Add(ctx, "blocked")
	require.NoError(t, err)
	prereq, err := svc.Add(ctx, "prereq")
	require.NoError(t, err)
	require.NoError(t, svc.AddDependency(ctx, blocked, prereq))
	require.NoError(t, svc.Fail(ctx, prereq, "boom"))

	ready, err := svc.Ready(ctx)
	require.NoError(t, err)
	for _, task := range ready {
		require.NotEqual(t, blocked, task.ID)
	}
}

func TestServiceReadyExcludesResourceLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	_, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "first", ResourceKey: "repo:x"})
	require.NoError(t, err)
	second, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "second", ResourceKey: "repo:x"})
	require.NoError(t, err)
	claimed, err := svc.Take(ctx, 0, "", "")
	require.NoError(t, err)
	require.NotNil(t, claimed)

	ready, err := svc.Ready(ctx)
	require.NoError(t, err)
	for _, task := range ready {
		require.NotEqual(t, second, task.ID)
	}
}

func TestServiceReadyKeepsExpiredResourceLeaseLockedUntilRequeue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := fixed
	svc := newServiceWithNow(t, func() time.Time { return now })

	_, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "first", ResourceKey: "repo:x"})
	require.NoError(t, err)
	second, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "second", ResourceKey: "repo:x"})
	require.NoError(t, err)
	claimed, err := svc.Take(ctx, time.Second, "", "")
	require.NoError(t, err)
	require.NotNil(t, claimed)

	now = now.Add(2 * time.Second)
	ready, err := svc.Ready(ctx)
	require.NoError(t, err)
	for _, task := range ready {
		require.NotEqual(t, second, task.ID)
	}

	_, err = svc.RequeueStale(ctx, time.Minute)
	require.NoError(t, err)
	ready, err = svc.Ready(ctx)
	require.NoError(t, err)
	require.Len(t, ready, 2)
	require.Equal(t, claimed.ID, ready[0].ID)
	require.Equal(t, second, ready[1].ID)
}

func TestServiceNextAndPopEmptyQueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	next, err := svc.Next(ctx)
	require.NoError(t, err)
	require.Nil(t, next)

	popped, err := svc.Take(ctx, 0, "", "")
	require.NoError(t, err)
	require.Nil(t, popped)
}

func TestServiceNextAgreesWithTake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	first, err := svc.Add(ctx, "first")
	require.NoError(t, err)
	_, err = svc.Add(ctx, "second")
	require.NoError(t, err)

	next, err := svc.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, first, next.ID)

	popped, err := svc.Take(ctx, 0, "", "")
	require.NoError(t, err)
	require.NotNil(t, popped)
	require.Equal(t, next.ID, popped.ID)
}

func TestServiceNextAndPopAgreeWithPriorityOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	_, err := svc.Add(ctx, "normal")
	require.NoError(t, err)
	urgent, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "urgent", Priority: "urgent"})
	require.NoError(t, err)

	next, err := svc.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, urgent, next.ID)

	popped, err := svc.Take(ctx, 0, "", "")
	require.NoError(t, err)
	require.NotNil(t, popped)
	require.Equal(t, urgent, popped.ID)
}

func TestServicePopRecordsWorker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "worker")
	require.NoError(t, err)
	popped, err := svc.Take(ctx, 0, "worker-1", "codex")
	require.NoError(t, err)
	require.Equal(t, id, popped.ID)

	data, err := svc.Explain(ctx, id)
	require.NoError(t, err)
	require.Len(t, data.Attempts, 1)
	require.Equal(t, "worker-1", data.Attempts[0].WorkerID)
	require.Equal(t, "codex", data.Attempts[0].Agent)
}

func TestServiceRequeueStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := fixed
	svc := newServiceWithNow(t, func() time.Time { return now })

	stale, err := svc.Add(ctx, "stale")
	require.NoError(t, err)
	claimed, err := svc.Take(ctx, time.Nanosecond, "", "")
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, stale, claimed.ID)
	now = now.Add(time.Second)
	requeued, err := svc.RequeueStale(ctx, time.Hour)
	require.NoError(t, err)
	require.Len(t, requeued, 1)
	require.Equal(t, stale, requeued[0].ID)

	claimed, err = svc.Take(ctx, 0, "recovery-worker", "")
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, stale, claimed.ID)

	require.NoError(t, svc.SetStatusWithStageWorker(ctx, stale, task.StatusDone, "stale recovery verified", nil, "recovery-worker"))
	got, err := svc.Show(ctx, stale)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)
}

func TestServiceHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "heartbeat")
	require.NoError(t, err)
	_, err = svc.Take(ctx, time.Minute, "worker-1", "codex")
	require.NoError(t, err)
	require.NoError(t, svc.Heartbeat(ctx, id, "worker-1", 30*time.Minute))

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "2025-01-02T03:34:05Z", got.LeaseExpires)
}

func TestServiceMissingTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	_, err := svc.Show(ctx, "missing")
	require.Error(t, err)
	require.True(t, errors.Is(err, app.ErrNotFound))
	require.ErrorIs(t, svc.Remove(ctx, "missing"), app.ErrNotFound)
}

func TestServicePropagatesStoreErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	boom := errors.New("boom")
	svc := app.NewService(&errorStore{err: boom}, func() time.Time { return fixed })

	_, err := svc.Add(ctx, "valid task body")
	require.ErrorIs(t, err, boom)
	_, err = svc.List(ctx, "")
	require.ErrorIs(t, err, boom)
	_, err = svc.Find(ctx, "", "")
	require.ErrorIs(t, err, boom)
	_, err = svc.RecentPaths(ctx)
	require.ErrorIs(t, err, boom)
	_, err = svc.Show(ctx, "missing")
	require.ErrorIs(t, err, boom)
	_, err = svc.Count(ctx)
	require.ErrorIs(t, err, boom)
	_, err = svc.Status(ctx)
	require.ErrorIs(t, err, boom)
	_, err = svc.Next(ctx)
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, svc.Done(ctx, "id", "verified"), boom)
	require.ErrorIs(t, svc.Fail(ctx, "id", "reason"), boom)
	require.ErrorIs(t, svc.SetStatus(ctx, "id", task.StatusDone, "verified"), boom)
	require.ErrorIs(t, svc.Remove(ctx, "id"), boom)
	_, err = svc.Prune(ctx, []task.Status{task.StatusDone})
	require.ErrorIs(t, err, boom)
	_, err = svc.Take(ctx, 0, "", "")
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, svc.Heartbeat(ctx, "id", "worker", time.Minute), boom)
	_, err = svc.RequeueStale(ctx, time.Hour)
	require.ErrorIs(t, err, boom)
}

func TestServiceExplainPropagatesHistoryErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	boom := errors.New("boom")
	base := []task.Task{{ID: "id", Status: task.StatusTodo, Body: "body"}}

	svc := app.NewService(&historyErrorStore{tasks: base, eventsErr: boom}, func() time.Time { return fixed })
	_, err := svc.Explain(ctx, "id")
	require.ErrorIs(t, err, boom)

	svc = app.NewService(&historyErrorStore{tasks: base, attemptsErr: boom}, func() time.Time { return fixed })
	_, err = svc.Explain(ctx, "id")
	require.ErrorIs(t, err, boom)

	svc = app.NewService(&historyErrorStore{tasks: base, depsErr: boom}, func() time.Time { return fixed })
	_, err = svc.Show(ctx, "id")
	require.ErrorIs(t, err, boom)
}

func TestServiceRecordsRejectionToSidecar(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sidecar := newServiceWithSidecar(t)

	badOpts := task.AddOptions{
		Body:   "do something vague",
		Tags:   []string{"discovery"},
		Source: "discovery",
		CWD:    "/tmp",
	}
	_, err := svc.AddWithOptions(ctx, badOpts)
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask))

	data, readErr := os.ReadFile(sidecar)
	require.NoError(t, readErr)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 1)

	var rec app.RejectionRecord
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &rec))
	require.Equal(t, err.Error(), rec.Reason)
	require.Equal(t, badOpts.Body, rec.Body)
	require.Equal(t, badOpts.Tags, rec.Tags)
	require.Equal(t, badOpts.Source, rec.Source)
	require.Equal(t, badOpts.CWD, rec.CWD)
	require.False(t, rec.Ts.IsZero())
}

func TestServiceAccumulatesRejections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sidecar := newServiceWithSidecar(t)

	badOpts := task.AddOptions{
		Body:   "do something vague",
		Tags:   []string{"discovery"},
		Source: "discovery",
		CWD:    "/tmp",
	}
	for range 3 {
		_, err := svc.AddWithOptions(ctx, badOpts)
		require.Error(t, err)
	}

	data, err := os.ReadFile(sidecar)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 3)
	for _, line := range lines {
		var rec app.RejectionRecord
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
	}
}

func TestRejectionSidecarTruncatesAndSkipsMalformedLines(t *testing.T) {
	t.Parallel()

	sidecar := filepath.Join(t.TempDir(), "rejected.jsonl")
	longBody := strings.Repeat("x", 6000)
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{
		Body:   longBody,
		Tags:   []string{"discovery"},
		Source: "task-discovery",
	}, errors.New("too vague"), fixed))
	require.NoError(t, os.WriteFile(sidecar, append([]byte("{not json}\n"), mustReadFile(t, sidecar)...), 0o600))

	records, err := app.ReadRejections(sidecar)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Contains(t, records[0].Body, "...[truncated]")
	require.LessOrEqual(t, len(records[0].Body), len(longBody))

	err = app.RecordRejection("", task.AddOptions{Body: "bad"}, errors.New("reason"), fixed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty sidecar path")
}

func TestRejectionSidecarWriteErrors(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(filePath, nil, 0o600))
	err := app.RecordRejection(filepath.Join(filePath, "rejected.jsonl"), task.AddOptions{Body: "bad"}, errors.New("reason"), fixed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mkdir")

	_, err = app.RemoveRejectionAt(filepath.Join(filePath, "child.jsonl"), 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read rejection sidecar")

	missingDirPath := filepath.Join(t.TempDir(), "missing", "rejected.jsonl")
	_, err = app.RemoveRejectionAt(missingDirPath, 0)
	require.ErrorIs(t, err, app.ErrRejectionIndexOutOfRange)
}

func TestServiceWithoutSidecarDoesNotWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	badOpts := task.AddOptions{
		Body:   "do something vague",
		Tags:   []string{"discovery"},
		Source: "discovery",
		CWD:    "/tmp",
	}
	_, err := svc.AddWithOptions(ctx, badOpts)
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask))
}

func TestSidecarPathDerivation(t *testing.T) {
	t.Parallel()
	paths := store.Paths{
		SQLitePath: "/home/user/.claude/queue/tasks.sqlite",
	}
	require.Equal(t, "/home/user/.claude/queue/rejected.jsonl", app.SidecarPath(paths))
	require.Equal(t, "", app.SidecarPath(store.Paths{}))
}

func TestListRejectedEmptyOrMissingReturnsNilNoError(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithSidecar(t)
	got, err := svc.ListRejected()
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestListRejectedReturnsRecordsInOrder(t *testing.T) {
	t.Parallel()
	svc, sidecar := newServiceWithSidecar(t)
	now := time.Now()
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{Body: "first bad task"}, errors.New("reason A"), now))
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{Body: "second bad task"}, errors.New("reason B"), now.Add(time.Second)))
	got, err := svc.ListRejected()
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "first bad task", got[0].Body)
	require.Equal(t, "second bad task", got[1].Body)
}

func TestRemoveRejectedDropsCorrectEntry(t *testing.T) {
	t.Parallel()
	svc, sidecar := newServiceWithSidecar(t)
	now := time.Now()
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{Body: "keep this"}, errors.New("r1"), now))
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{Body: "drop this"}, errors.New("r2"), now))
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{Body: "also keep"}, errors.New("r3"), now))

	removed, err := svc.RemoveRejected(1) // 0-based -> middle entry
	require.NoError(t, err)
	require.Equal(t, "drop this", removed.Body)

	remaining, err := svc.ListRejected()
	require.NoError(t, err)
	require.Len(t, remaining, 2)
	require.Equal(t, "keep this", remaining[0].Body)
	require.Equal(t, "also keep", remaining[1].Body)
}

func TestRemoveRejectedOutOfRangeReturnsSentinel(t *testing.T) {
	t.Parallel()
	svc, sidecar := newServiceWithSidecar(t)
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{Body: "only one"}, errors.New("r"), time.Now()))
	_, err := svc.RemoveRejected(5)
	require.ErrorIs(t, err, app.ErrRejectionIndexOutOfRange)
}

func TestRejectedMethodsErrorWhenSidecarDisabled(t *testing.T) {
	t.Parallel()
	// Construct a service WITHOUT WithSidecarPath — newService builds one this way.
	svc := newService(t)
	_, err := svc.ListRejected()
	require.ErrorIs(t, err, app.ErrSidecarDisabled)
	_, err = svc.RemoveRejected(0)
	require.ErrorIs(t, err, app.ErrSidecarDisabled)
	_, err = svc.RetryRejected(context.Background(), 0)
	require.ErrorIs(t, err, app.ErrSidecarDisabled)
}

func TestRetryRejectedPreservesCapturedMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sidecar := newServiceWithSidecar(t)
	opts := task.AddOptions{
		Body:        validDiscoveryBody("/tmp/repo/file.go"),
		Priority:    "high",
		Tags:        []string{"discovery"},
		CWD:         "/tmp/repo",
		Source:      "task-discovery",
		Agent:       "codex",
		GroupID:     "group-1",
		ResourceKey: "repo:/tmp/repo",
	}
	require.NoError(t, app.RecordRejection(sidecar, opts, errors.New("previous validation failure"), fixed))

	created, err := svc.RetryRejected(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, opts.Body, created.Body)
	require.Equal(t, opts.Priority, created.Priority)
	require.Equal(t, opts.Tags, created.Tags)
	require.Equal(t, opts.CWD, created.CWD)
	require.Equal(t, opts.Source, created.Source)
	require.Equal(t, opts.Agent, created.Agent)
	require.Equal(t, opts.GroupID, created.GroupID)
	require.Equal(t, opts.ResourceKey, created.ResourceKey)

	remaining, err := svc.ListRejected()
	require.NoError(t, err)
	require.Empty(t, remaining)
}

func TestRetryRejectedSupportsOldSidecarRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sidecar := newServiceWithSidecar(t)
	require.NoError(t, os.WriteFile(sidecar, []byte(`{"ts":"2025-01-02T03:04:05Z","reason":"old","body":"fix the focused software issue"}`+"\n"), 0o600))

	created, err := svc.RetryRejected(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, "fix the focused software issue", created.Body)
	require.Empty(t, created.Priority)
	require.Empty(t, created.ResourceKey)
}

func TestRetryRejectedLeavesInvalidRecordInPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sidecar := newServiceWithSidecar(t)
	require.NoError(t, app.RecordRejection(sidecar, task.AddOptions{Body: "pick my nose"}, errors.New("invalid"), fixed))

	_, err := svc.RetryRejected(ctx, 0)
	require.ErrorIs(t, err, task.ErrInvalidTask)
	records, readErr := svc.ListRejected()
	require.NoError(t, readErr)
	require.Len(t, records, 2)
	require.Equal(t, "pick my nose", records[0].Body)
	require.Equal(t, "pick my nose", records[1].Body)
}

func TestAddWithOptionsForceStillRejectsInvalidPriority(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceWithSidecar(t)
	_, err := svc.AddWithOptionsForce(context.Background(), task.AddOptions{
		Body:     "valid body",
		Priority: "later",
	})
	require.ErrorIs(t, err, task.ErrInvalidPriority)
}

func TestAddWithOptionsForceValidTaskBehavesLikeNormalAdd(t *testing.T) {
	t.Parallel()
	svc, sidecar := newServiceWithSidecar(t)
	opts := task.AddOptions{Body: "a normal valid task body that passes validation"}
	require.NoError(t, task.ValidateAddOptions(opts))
	id, err := svc.AddWithOptionsForce(context.Background(), opts)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	records, _ := app.ReadRejections(sidecar)
	require.Empty(t, records)
}

func TestAddWithOptionsForceInvalidTaskInsertsAndRecordsSidecar(t *testing.T) {
	t.Parallel()
	svc, sidecar := newServiceWithSidecar(t)
	opts := task.AddOptions{Body: "fix the thing"} // matches invalidExactBodies
	require.ErrorIs(t, task.ValidateAddOptions(opts), task.ErrInvalidTask)
	id, err := svc.AddWithOptionsForce(context.Background(), opts)
	require.NoError(t, err, "force-add must succeed even when validation rejects")
	require.NotEmpty(t, id)
	records, err := app.ReadRejections(sidecar)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "fix the thing", records[0].Body)
}

func TestServiceAddDependencyRejectsCycles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	a, err := svc.Add(ctx, "task a")
	require.NoError(t, err)
	b, err := svc.Add(ctx, "task b")
	require.NoError(t, err)

	// A depends on B — valid, no cycle.
	require.NoError(t, svc.AddDependency(ctx, a, b))

	// B depends on A — closes the cycle; must be rejected.
	err = svc.AddDependency(ctx, b, a)
	require.Error(t, err)
	require.True(t, errors.Is(err, store.ErrDependencyCycle), "expected ErrDependencyCycle, got %v", err)
}

func TestDone_WithNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "task with note")
	require.NoError(t, err)
	require.NoError(t, svc.Done(ctx, id, "completed the thing"))

	data, err := svc.Explain(ctx, id)
	require.NoError(t, err)
	var found bool
	for _, e := range data.Events {
		if e.Type == task.EventDone {
			require.Equal(t, "completed the thing", e.Message)
			found = true
		}
	}
	require.True(t, found, "expected an EventDone event")
}

func TestDoneRequiresNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "task without note")
	require.NoError(t, err)
	_, err = svc.Take(ctx, 0, "", "")
	require.NoError(t, err)

	err = svc.Done(ctx, id, "")
	require.ErrorIs(t, err, task.ErrMissingCompletionNote)

	data, err := svc.Explain(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusDoing, data.Task.Status)
	var found bool
	for _, e := range data.Events {
		if e.Type == task.EventDone {
			found = true
		}
	}
	require.False(t, found, "empty completion evidence must not record EventDone")
}

type errorStore struct {
	app.Store
	err error
}

func (s *errorStore) List(context.Context) ([]task.Task, error) { return nil, s.err }
func (s *errorStore) Get(context.Context, string) (task.Task, error) {
	return task.Task{}, s.err
}
func (s *errorStore) Counts(context.Context) (map[task.Status]int, error) {
	return nil, s.err
}
func (s *errorStore) ActiveLists(context.Context) ([]task.Task, []task.Task, error) {
	return nil, nil, s.err
}
func (s *errorStore) Ready(context.Context) ([]task.Task, error) { return nil, s.err }
func (s *errorStore) Add(context.Context, task.Task) error       { return s.err }
func (s *errorStore) Update(context.Context, string, task.EventType, string, func(*task.Task) bool) error {
	return s.err
}

func (s *errorStore) UpdateGuarded(context.Context, string, task.EventType, string, func(*task.Task) bool) error {
	return s.err
}
func (s *errorStore) Delete(context.Context, string) error              { return s.err }
func (s *errorStore) Prune(context.Context, []task.Status) (int, error) { return 0, s.err }
func (s *errorStore) ClaimNextForWorker(context.Context, time.Time, time.Time, string, string) (*task.Task, error) {
	return nil, s.err
}
func (s *errorStore) Heartbeat(context.Context, string, string, time.Time, time.Time) error {
	return s.err
}
func (s *errorStore) RequeueStale(context.Context, time.Duration, time.Time) ([]task.Task, error) {
	return nil, s.err
}
func (s *errorStore) RecentDistinctCWDs(context.Context, int) ([]string, error) {
	return nil, s.err
}

type historyErrorStore struct {
	app.Store
	tasks       []task.Task
	depsErr     error
	eventsErr   error
	attemptsErr error
}

func (s *historyErrorStore) List(context.Context) ([]task.Task, error) {
	return append([]task.Task(nil), s.tasks...), nil
}

func (s *historyErrorStore) Get(_ context.Context, id string) (task.Task, error) {
	for _, t := range s.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return task.Task{}, app.ErrNotFound
}

func (s *historyErrorStore) Dependencies(context.Context, string) ([]task.Dependency, error) {
	return nil, s.depsErr
}

func (s *historyErrorStore) Events(context.Context, string) ([]task.Event, error) {
	return nil, s.eventsErr
}

func (s *historyErrorStore) Attempts(context.Context, string) ([]task.Attempt, error) {
	return nil, s.attemptsErr
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
