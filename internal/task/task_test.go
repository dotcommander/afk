package task_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestTransitions(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("test", -5*60*60))
	wantUTC := "2025-01-02T08:04:05Z"

	tk := task.Task{Status: task.StatusPending}
	tk.MarkWorking(now)
	require.Equal(t, task.StatusWorking, tk.Status)
	require.Equal(t, wantUTC, tk.Started)

	require.True(t, tk.MarkDone(now))
	require.Equal(t, task.StatusDone, tk.Status)
	require.Equal(t, wantUTC, tk.Finished)
	require.False(t, tk.MarkDone(now))

	tk.Reset()
	require.Equal(t, task.StatusPending, tk.Status)
	require.Empty(t, tk.Started)
	require.Empty(t, tk.Finished)
	require.Empty(t, tk.Error)

	require.True(t, tk.MarkFailed(now, "boom"))
	require.Equal(t, task.StatusFailed, tk.Status)
	require.Equal(t, "boom", tk.Error)
	require.False(t, tk.MarkFailed(now, "ignored"))
	require.Equal(t, "boom", tk.Error)
}

func TestTaskMetadataJSON(t *testing.T) {
	t.Parallel()

	tk := task.Task{
		ID:          "1",
		Created:     "2025-01-02T03:04:05Z",
		Status:      task.StatusPending,
		Body:        "body",
		Priority:    "high",
		Tags:        []string{"repo:afk", "type:test"},
		CWD:         "/tmp/repo",
		Source:      "cli",
		Agent:       "codex",
		GroupID:     "g1",
		ResourceKey: "repo:/tmp/repo",
	}
	b, err := json.Marshal(tk)
	require.NoError(t, err)
	require.Contains(t, string(b), `"cwd":"/tmp/repo"`)
	require.Contains(t, string(b), `"tags":["repo:afk","type:test"]`)

	var got task.Task
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, tk.CWD, got.CWD)
	require.Equal(t, tk.Tags, got.Tags)
}

func TestValidStatus(t *testing.T) {
	t.Parallel()

	for _, status := range task.OrderedStatuses() {
		require.True(t, task.ValidStatus(status))
	}
	require.False(t, task.ValidStatus("weird"))
}
