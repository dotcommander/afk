package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestRecentPathsSortsDistinctRecentPaths(t *testing.T) {
	t.Parallel()

	tasks := []task.Task{
		{Created: "2025-01-02T03:00:00Z", CWD: "/old"},
		{Created: "2025-01-02T03:04:00Z", CWD: "/new-b"},
		{Created: "2025-01-02T03:03:00Z", CWD: "/new-a"},
		{Created: "2025-01-02T03:02:00Z", CWD: "/new-b"},
		{Created: "2025-01-02T03:05:00Z"},
	}

	require.Equal(t, []string{"/new-a", "/new-b"}, recentPaths(tasks, 2))
	require.Equal(t, []string{"/new-a", "/new-b", "/old"}, recentPaths(tasks, 0))
}

func TestFilterStatusAndTaskMatches(t *testing.T) {
	t.Parallel()

	tasks := []task.Task{
		{ID: "1", Status: task.StatusTodo, Body: "alpha", CWD: "/repo/a", Tags: []string{"repo:one"}},
		{ID: "2", Status: task.StatusDoing, Body: "beta", ResourceKey: "repo:/repo/b"},
		{ID: "3", Status: task.StatusDeleted, Body: "deleted", Error: "gone"},
	}

	require.Len(t, filterByStatus(tasks, ""), 2)
	require.Len(t, filterByStatus(tasks, "all"), 3)
	require.Len(t, filterByStatus(tasks, "todo"), 1)
	require.Len(t, filterByStatus(tasks, "pending"), 1)
	require.Empty(t, filterByStatus(tasks, "nope"))
	require.NoError(t, validateStatusFilter(""))
	require.NoError(t, validateStatusFilter("all"))
	require.NoError(t, validateStatusFilter("working"))
	require.ErrorIs(t, validateStatusFilter("nope"), task.ErrInvalidStatus)
	require.True(t, taskMatches(tasks[0], "repo:one"))
	require.True(t, taskMatches(tasks[1], "repo:/repo/b"))
	require.True(t, taskMatches(tasks[2], "gone"))
	require.False(t, taskMatches(tasks[0], "missing"))
}

func TestInferAddDefaultsFindsGitRoot(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o755))
	nested := filepath.Join(repo, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	file := filepath.Join(nested, "file.go")
	require.NoError(t, os.WriteFile(file, []byte("package test\n"), 0o644))

	defaults := InferAddDefaults(file)
	require.Equal(t, "repo:"+filepath.Base(repo), defaults.RepoTag)
	require.Equal(t, "repo:"+repo, defaults.ResourceKey)
	require.Equal(t, AddDefaults{}, InferAddDefaults(""))
	require.Equal(t, AddDefaults{}, InferAddDefaults(filepath.Join(repo, "missing")))
}

func TestLeaseAndWorkerHelpers(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	require.True(t, leaseExpires(now, 0).IsZero())
	require.Equal(t, now.Add(time.Minute), leaseExpires(now, time.Minute))
	require.Equal(t, "named", workerOrDefault("named"))

	require.NotEmpty(t, workerOrDefault(""))
	require.Contains(t, defaultWorkerID(), ":")
}

func TestEventForStatus(t *testing.T) {
	t.Parallel()

	require.Equal(t, task.EventDone, eventForStatus(task.StatusDone))
	require.Equal(t, task.EventFailed, eventForStatus(task.StatusFailed))
	require.Equal(t, task.EventDeleted, eventForStatus(task.StatusDeleted))
	require.Equal(t, task.EventClaimed, eventForStatus(task.StatusDoing))
	require.Equal(t, task.EventRequeued, eventForStatus(task.StatusTodo))
	require.PanicsWithValue(t,
		"task: EventForStatus called with unknown status unknown — callers must validate via ParseStatus first",
		func() { _ = eventForStatus(task.Status("unknown")) },
		"unknown status should panic (callers must validate via task.ParseStatus first)")
}
