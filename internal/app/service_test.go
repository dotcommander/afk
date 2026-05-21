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
	require.Equal(t, task.StatusPending, tasks[0].Status)

	next, err := svc.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, id, next.ID)

	popped, err := svc.Pop(ctx)
	require.NoError(t, err)
	require.NotNil(t, popped)
	require.Equal(t, task.StatusWorking, popped.Status)
	require.Equal(t, fixed.Format(time.RFC3339), popped.Started)

	require.NoError(t, svc.Done(ctx, id, ""))
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)

	require.NoError(t, svc.Reset(ctx, id))
	require.NoError(t, svc.Fail(ctx, id, "oops"))
	tally, err := svc.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, tally[task.StatusFailed])

	pruned, err := svc.Prune(ctx, []task.Status{task.StatusFailed})
	require.NoError(t, err)
	require.Equal(t, 1, pruned)
	tasks, err = svc.List(ctx, "")
	require.NoError(t, err)
	require.Empty(t, tasks)
}

func TestServiceFilteringEditingAndCollisionIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	first, err := svc.Add(ctx, "first")
	require.NoError(t, err)
	second, err := svc.Add(ctx, "second")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.True(t, strings.HasSuffix(second, "-1"))

	require.NoError(t, svc.Edit(ctx, second, "edited"))
	pending, err := svc.List(ctx, string(task.StatusPending))
	require.NoError(t, err)
	require.Len(t, pending, 2)
	require.Equal(t, "edited", pending[1].Body)

	done, err := svc.List(ctx, string(task.StatusDone))
	require.NoError(t, err)
	require.Empty(t, done)
}

func TestServiceEditPreservesGeneratedTaskValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.AddWithOptions(ctx, task.AddOptions{
		Body:   validDiscoveryBody("/tmp/repo/file.go"),
		Tags:   []string{"discovery"},
		CWD:    "/tmp/repo",
		Source: "task-discovery",
	})
	require.NoError(t, err)

	err = svc.Edit(ctx, id, "fix focused software issue")
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, validDiscoveryBody("/tmp/repo/file.go"), got.Body)
}

func TestServiceRetriesDuplicateIDOnAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := &duplicateOnFirstAddStore{}
	svc := app.NewService(st, func() time.Time { return fixed })

	id, err := svc.Add(ctx, "retry duplicate id")
	require.NoError(t, err)
	require.Equal(t, "1735787045-1", id)
	require.Equal(t, 2, st.addCalls)
}

type duplicateOnFirstAddStore struct {
	app.Store
	tasks    []task.Task
	addCalls int
}

func (s *duplicateOnFirstAddStore) List(context.Context) ([]task.Task, error) {
	return append([]task.Task(nil), s.tasks...), nil
}

func (s *duplicateOnFirstAddStore) Add(_ context.Context, t task.Task) error {
	s.addCalls++
	if s.addCalls == 1 {
		s.tasks = append(s.tasks, task.Task{ID: t.ID})
		return store.ErrDuplicateTask
	}
	s.tasks = append(s.tasks, t)
	return nil
}

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
	require.Error(t, svc.Edit(ctx, id, "make it better"))
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
	require.Equal(t, "high", got.Priority)
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

	require.NoError(t, svc.RemoveDependency(ctx, blocked, prereq))
	deps, err = svc.Dependencies(ctx, blocked)
	require.NoError(t, err)
	require.Empty(t, deps)
}

func TestServiceReadyAndWhy(t *testing.T) {
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

	why, err := svc.Why(ctx, blocked)
	require.NoError(t, err)
	require.False(t, why.Ready)
	require.Len(t, why.Reasons, 1)
	require.Equal(t, "dependency_pending", why.Reasons[0].Kind)
	require.Equal(t, prereq, why.Reasons[0].Detail)

	require.NoError(t, svc.Done(ctx, prereq, ""))
	why, err = svc.Why(ctx, blocked)
	require.NoError(t, err)
	require.True(t, why.Ready)
	require.Empty(t, why.Reasons)
}

func TestServiceWhyReportsFailedDependency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	blocked, err := svc.Add(ctx, "blocked")
	require.NoError(t, err)
	prereq, err := svc.Add(ctx, "prereq")
	require.NoError(t, err)
	require.NoError(t, svc.AddDependency(ctx, blocked, prereq))
	require.NoError(t, svc.Fail(ctx, prereq, "boom"))

	why, err := svc.Why(ctx, blocked)
	require.NoError(t, err)
	require.False(t, why.Ready)
	require.Len(t, why.Reasons, 1)
	require.Equal(t, "dependency_failed", why.Reasons[0].Kind)
	require.Equal(t, prereq, why.Reasons[0].Detail)
}

func TestServiceManualBlockReadiness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "blocked")
	require.NoError(t, err)
	require.NoError(t, svc.Block(ctx, id, "waiting"))

	ready, err := svc.Ready(ctx)
	require.NoError(t, err)
	require.Empty(t, ready)

	why, err := svc.Why(ctx, id)
	require.NoError(t, err)
	require.False(t, why.Ready)
	require.Len(t, why.Reasons, 1)
	require.Equal(t, "manual_block", why.Reasons[0].Kind)
	require.Equal(t, "waiting", why.Reasons[0].Detail)

	require.NoError(t, svc.Unblock(ctx, id))
	why, err = svc.Why(ctx, id)
	require.NoError(t, err)
	require.True(t, why.Ready)
}

func TestServiceWhyReportsResourceLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	_, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "first", ResourceKey: "repo:x"})
	require.NoError(t, err)
	second, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "second", ResourceKey: "repo:x"})
	require.NoError(t, err)
	claimed, err := svc.Pop(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)

	why, err := svc.Why(ctx, second)
	require.NoError(t, err)
	require.False(t, why.Ready)
	require.Len(t, why.Reasons, 1)
	require.Equal(t, "resource_locked", why.Reasons[0].Kind)
	require.Equal(t, claimed.ID, why.Reasons[0].Detail)
}

func TestServiceWhyKeepsExpiredResourceLeaseLockedUntilRequeue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := fixed
	svc := newServiceWithNow(t, func() time.Time { return now })

	_, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "first", ResourceKey: "repo:x"})
	require.NoError(t, err)
	second, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "second", ResourceKey: "repo:x"})
	require.NoError(t, err)
	claimed, err := svc.PopWithLease(ctx, time.Second)
	require.NoError(t, err)
	require.NotNil(t, claimed)

	now = now.Add(2 * time.Second)
	why, err := svc.Why(ctx, second)
	require.NoError(t, err)
	require.False(t, why.Ready)
	require.Len(t, why.Reasons, 1)
	require.Equal(t, "resource_locked", why.Reasons[0].Kind)
	require.Equal(t, claimed.ID, why.Reasons[0].Detail)

	_, err = svc.RequeueStale(ctx, time.Minute)
	require.NoError(t, err)
	why, err = svc.Why(ctx, second)
	require.NoError(t, err)
	require.True(t, why.Ready)
	require.Empty(t, why.Reasons)
}

func TestServiceNextAndPopEmptyQueue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	next, err := svc.Next(ctx)
	require.NoError(t, err)
	require.Nil(t, next)

	popped, err := svc.Pop(ctx)
	require.NoError(t, err)
	require.Nil(t, popped)
}

func TestServiceNextAgreesWithPop(t *testing.T) {
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

	popped, err := svc.Pop(ctx)
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

	popped, err := svc.Pop(ctx)
	require.NoError(t, err)
	require.NotNil(t, popped)
	require.Equal(t, urgent, popped.ID)
}

func TestServicePromote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	first, err := svc.Add(ctx, "first")
	require.NoError(t, err)
	second, err := svc.Add(ctx, "second")
	require.NoError(t, err)

	require.NoError(t, svc.Promote(ctx, second))
	next, err := svc.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, second, next.ID)

	require.NoError(t, svc.Done(ctx, second, ""))
	require.ErrorIs(t, svc.Promote(ctx, second), store.ErrInvalidState)
	require.ErrorIs(t, svc.Promote(ctx, "missing"), store.ErrNotFound)
	next, err = svc.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, first, next.ID)
}

func TestServicePopRecordsWorker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "worker")
	require.NoError(t, err)
	popped, err := svc.PopWithLeaseForWorker(ctx, 0, "worker-1", "codex")
	require.NoError(t, err)
	require.Equal(t, id, popped.ID)

	data, err := svc.Explain(ctx, id)
	require.NoError(t, err)
	require.Len(t, data.Attempts, 1)
	require.Equal(t, "worker-1", data.Attempts[0].WorkerID)
	require.Equal(t, "codex", data.Attempts[0].Agent)
}

func TestServiceRetryRequeueStaleAndPruneByTag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := fixed
	svc := newServiceWithNow(t, func() time.Time { return now })

	failed, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "failed", Tags: []string{"spec:drop"}})
	require.NoError(t, err)
	require.NoError(t, svc.Fail(ctx, failed, "boom"))
	require.NoError(t, svc.Retry(ctx, failed))
	got, err := svc.Show(ctx, failed)
	require.NoError(t, err)
	require.Equal(t, task.StatusPending, got.Status)

	stale, err := svc.Add(ctx, "stale")
	require.NoError(t, err)
	claimed, err := svc.PopWithLease(ctx, time.Nanosecond)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, failed, claimed.ID)
	now = now.Add(time.Second)
	requeued, err := svc.RequeueStale(ctx, time.Hour)
	require.NoError(t, err)
	require.Len(t, requeued, 1)
	require.Equal(t, failed, requeued[0].ID)

	claimed, err = svc.Pop(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, failed, claimed.ID)
	require.NoError(t, svc.Done(ctx, failed, ""))
	claimed, err = svc.Pop(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, stale, claimed.ID)

	n, err := svc.PruneByTag(ctx, "spec:drop")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	_, err = svc.Show(ctx, failed)
	require.ErrorIs(t, err, app.ErrNotFound)
}

func TestServiceRetryRejectsInvalidFailedBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.AddWithOptionsForce(ctx, task.AddOptions{
		Body: "HITL GATE FIRST: post this question to the user via AskUserQuestion.",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Fail(ctx, id, "invalid autonomy contract"))

	err = svc.Retry(ctx, id)
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status)
}

func TestServiceRetryRejectsInvalidGeneratedTaskShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.AddWithOptionsForce(ctx, task.AddOptions{
		Body:   "[discovery:repo:file] fix focused software issue",
		Tags:   []string{"discovery"},
		CWD:    "/tmp/repo",
		Source: "task-discovery",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Fail(ctx, id, "invalid discovery contract"))

	err = svc.Retry(ctx, id)
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status)
}

func TestServiceHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "heartbeat")
	require.NoError(t, err)
	_, err = svc.PopWithLeaseForWorker(ctx, time.Minute, "worker-1", "codex")
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

func TestServiceImportRequiresSuccessAndVerifySections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing success",
			body:    "Add the ledger.\n\nVerify:\n- go test ./internal/store/...",
			wantErr: `import: task "ledger": missing Success section`,
		},
		{
			name:    "missing verify",
			body:    "Add the ledger.\n\nSuccess:\n- Duplicate remote imports are ignored.",
			wantErr: `import: task "ledger": missing Verify section`,
		},
		{
			name:    "inline words do not count",
			body:    "Add the ledger. Success: dedupe works. Verify: run tests.",
			wantErr: `import: task "ledger": missing Success section`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newService(t)
			_, err := svc.Import(ctx, task.ImportDoc{Tasks: []task.ImportTask{{
				Slug: "ledger",
				Body: tt.body,
			}}})
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestServiceImportStoresTasksWithSuccessAndVerifySections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	results, err := svc.Import(ctx, task.ImportDoc{Tasks: []task.ImportTask{
		{
			Slug: "inspect",
			Body: "Inspect import code in /tmp/example.\n\nEvidence:\n- /tmp/example/import.go is the import surface.\n\nScope:\n- /tmp/example/import.go.\n\nSuccess:\n- Import extension points are listed.\n\nVerify:\n- Include file references in the summary.\n\nReject-if:\n- /tmp/example/import.go is unavailable.",
			Tags: []string{"spec:remote-drain"},
		},
		{
			Slug:      "implement",
			Body:      "Implement import validation in /tmp/example.\n\nEvidence:\n- /tmp/example/import.go performs import validation.\n\nScope:\n- /tmp/example/import.go.\n\nSuccess:\n- Missing sections are rejected.\n\nVerify:\n- go test ./internal/app/...\n\nReject-if:\n- /tmp/example/import.go is unavailable.",
			Tags:      []string{"spec:remote-drain"},
			BlockedBy: []string{"inspect"},
		},
	}})
	require.NoError(t, err)
	require.Len(t, results, 2)

	tasks, err := svc.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Contains(t, tasks[0].Body, "Success:\n- Import extension points are listed.")
	require.Contains(t, tasks[1].Body, "Verify:\n- go test ./internal/app/...")

	deps, err := svc.Dependencies(ctx, results[1].ID)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, results[0].ID, deps[0].DependsOnID)
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
	_, err = svc.Pop(ctx)
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

func TestDone_WithoutNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "task without note")
	require.NoError(t, err)
	_, err = svc.Pop(ctx)
	require.NoError(t, err)

	require.NoError(t, svc.Done(ctx, id, ""))

	data, err := svc.Explain(ctx, id)
	require.NoError(t, err)
	var found bool
	for _, e := range data.Events {
		if e.Type == task.EventDone {
			require.Equal(t, "", e.Message)
			found = true
		}
	}
	require.True(t, found, "expected an EventDone event")
}

// TestWhy_ReadyMatchesStoreReady asserts the invariant: Why(id).Ready == (id is present in Ready(ctx) output).
func TestWhy_ReadyMatchesStoreReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("plain pending task is ready", func(t *testing.T) {
		t.Parallel()
		svc := newService(t)

		id, err := svc.Add(ctx, "independent pending task")
		require.NoError(t, err)

		why, err := svc.Why(ctx, id)
		require.NoError(t, err)
		require.True(t, why.Ready)

		ready, err := svc.Ready(ctx)
		require.NoError(t, err)
		found := false
		for _, r := range ready {
			if r.ID == id {
				found = true
				break
			}
		}
		require.True(t, found, "id must be present in Ready() output when Why().Ready is true")
	})

	t.Run("pending task blocked by unfinished dep is not ready", func(t *testing.T) {
		t.Parallel()
		svc := newService(t)

		prereq, err := svc.Add(ctx, "prereq task")
		require.NoError(t, err)
		blocked, err := svc.Add(ctx, "blocked downstream task")
		require.NoError(t, err)
		require.NoError(t, svc.AddDependency(ctx, blocked, prereq))

		why, err := svc.Why(ctx, blocked)
		require.NoError(t, err)
		require.False(t, why.Ready)
		require.NotEmpty(t, why.Reasons, "Reasons must be non-empty when blocked by a dependency")
		require.Equal(t, "dependency_pending", why.Reasons[0].Kind)

		ready, err := svc.Ready(ctx)
		require.NoError(t, err)
		for _, r := range ready {
			require.NotEqual(t, blocked, r.ID, "blocked id must be absent from Ready() output")
		}
	})

	t.Run("done task is not ready", func(t *testing.T) {
		t.Parallel()
		svc := newService(t)

		id, err := svc.Add(ctx, "task to complete")
		require.NoError(t, err)
		require.NoError(t, svc.Done(ctx, id, ""))

		why, err := svc.Why(ctx, id)
		require.NoError(t, err)
		require.False(t, why.Ready)

		ready, err := svc.Ready(ctx)
		require.NoError(t, err)
		for _, r := range ready {
			require.NotEqual(t, id, r.ID, "done id must be absent from Ready() output")
		}
	})
}
