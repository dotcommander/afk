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
	require.Empty(t, tk.Error)
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

func TestStatusHelpersNormalizeLegacyValues(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   string
		want task.Status
	}{
		{in: "todo", want: task.StatusPending},
		{in: "pending", want: task.StatusPending},
		{in: "doing", want: task.StatusWorking},
		{in: "working", want: task.StatusWorking},
		{in: "done", want: task.StatusDone},
		{in: "failed", want: task.StatusFailed},
		{in: "deleted", want: task.StatusDeleted},
	} {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, ok := task.ParseStatus(tt.in)
			require.True(t, ok)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.want, task.NormalizeStatus(task.Status(tt.in)))
		})
	}

	unknown, ok := task.ParseStatus("mystery")
	require.False(t, ok)
	require.Empty(t, unknown)
	require.Equal(t, task.Status("mystery"), task.NormalizeStatus("mystery"))
	require.True(t, task.VisibleStatus(task.StatusPending))
	require.False(t, task.VisibleStatus(task.StatusDeleted))
	require.True(t, task.ActiveStatus(task.StatusPending))
	require.True(t, task.ActiveStatus(task.StatusWorking))
	require.False(t, task.ActiveStatus(task.StatusDone))
}

func TestSetStatusAndLeaseHelpers(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	tk := task.Task{Status: task.StatusPending}
	tk.SetLease(now)
	require.Equal(t, "2025-01-02T03:04:05Z", tk.LeaseExpires)
	tk.SetLease(time.Time{})
	require.Empty(t, tk.LeaseExpires)

	require.True(t, tk.SetStatus(task.StatusWorking, now, ""))
	require.Equal(t, task.StatusWorking, tk.Status)
	require.False(t, tk.SetStatus(task.StatusWorking, now, ""))
	require.True(t, tk.SetStatus(task.StatusDeleted, now, "obsolete"))
	require.Equal(t, task.StatusDeleted, tk.Status)
	require.Equal(t, "obsolete", tk.Error)
	require.False(t, tk.SetStatus(task.StatusDeleted, now, "ignored"))
	require.True(t, tk.SetStatus(task.StatusPending, now, "retry"))
	require.Equal(t, task.StatusPending, tk.Status)
	require.Empty(t, tk.Error)
	require.False(t, tk.SetStatus(task.StatusPending, now, "still todo"))
	require.True(t, tk.SetStatus(task.StatusDone, now, "done"))
	require.Empty(t, tk.Error)
	require.True(t, tk.SetStatus(task.StatusFailed, now, "boom"))
	require.Equal(t, "boom", tk.Error)
	require.True(t, tk.SetStatus(task.StatusWorking, now, "retry"))
	require.Empty(t, tk.Error)
	require.Empty(t, tk.Finished)
	require.False(t, tk.SetStatus("not-real", now, ""))
}

func TestAddOptionsFromTask(t *testing.T) {
	t.Parallel()

	tk := task.Task{
		Body:        "body",
		Priority:    "high",
		Tags:        []string{"repo:afk"},
		CWD:         "/tmp/repo",
		Source:      "cli",
		Agent:       "codex",
		GroupID:     "group",
		ResourceKey: "repo:/tmp/repo",
	}

	opts := task.AddOptionsFromTask(tk)
	require.Equal(t, tk.Body, opts.Body)
	require.Equal(t, tk.Priority, opts.Priority)
	require.Equal(t, tk.Tags, opts.Tags)
	require.Equal(t, tk.CWD, opts.CWD)
	require.Equal(t, tk.Source, opts.Source)
	require.Equal(t, tk.Agent, opts.Agent)
	require.Equal(t, tk.GroupID, opts.GroupID)
	require.Equal(t, tk.ResourceKey, opts.ResourceKey)
}
