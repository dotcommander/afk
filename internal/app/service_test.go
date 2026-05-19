package app_test

import (
	"context"
	"errors"
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
	s, err := store.NewSQLite(context.Background(), store.Paths{
		SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite"),
		JSONLPath:  filepath.Join(t.TempDir(), "tasks.jsonl"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return app.NewService(s, func() time.Time { return fixed })
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

	require.NoError(t, svc.Done(ctx, id))
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)

	require.NoError(t, svc.Reset(ctx, id))
	require.NoError(t, svc.Fail(ctx, id, "oops"))
	tally, err := svc.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, tally[task.StatusFailed])

	require.NoError(t, svc.Prune(ctx, []task.Status{task.StatusFailed}))
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

	require.NoError(t, svc.Done(ctx, prereq))
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
		tt := tt
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
			Body: "Inspect import code.\n\nSuccess:\n- Import extension points are listed.\n\nVerify:\n- Include file references in the summary.",
			Tags: []string{"spec:remote-drain"},
		},
		{
			Slug:      "implement",
			Body:      "Implement import validation.\n\nSuccess:\n- Missing sections are rejected.\n\nVerify:\n- go test ./internal/app/...",
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
