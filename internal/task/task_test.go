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

	tk := task.Task{Status: task.StatusTodo}
	tk.MarkWorking(now)
	require.Equal(t, task.StatusDoing, tk.Status)
	require.Equal(t, wantUTC, tk.Started)

	require.True(t, tk.MarkDone(now))
	require.Equal(t, task.StatusDone, tk.Status)
	require.Equal(t, wantUTC, tk.Finished)
	require.Empty(t, tk.Error)
	require.False(t, tk.MarkDone(now))

	tk.Reset()
	require.Equal(t, task.StatusTodo, tk.Status)
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
		Status:      task.StatusTodo,
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
		{in: "todo", want: task.StatusTodo},
		{in: "pending", want: task.StatusTodo},
		{in: "doing", want: task.StatusDoing},
		{in: "working", want: task.StatusDoing},
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
	require.True(t, task.VisibleStatus(task.StatusTodo))
	require.False(t, task.VisibleStatus(task.StatusDeleted))
	require.True(t, task.ActiveStatus(task.StatusTodo))
	require.True(t, task.ActiveStatus(task.StatusDoing))
	require.False(t, task.ActiveStatus(task.StatusDone))
}

func TestSetStatusAndLeaseHelpers(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	tk := task.Task{Status: task.StatusTodo}
	tk.SetLease(now)
	require.Equal(t, "2025-01-02T03:04:05Z", tk.LeaseExpires)
	tk.SetLease(time.Time{})
	require.Empty(t, tk.LeaseExpires)

	require.True(t, tk.SetStatus(task.StatusDoing, now, ""))
	require.Equal(t, task.StatusDoing, tk.Status)
	require.False(t, tk.SetStatus(task.StatusDoing, now, ""))
	require.True(t, tk.SetStatus(task.StatusDeleted, now, "obsolete"))
	require.Equal(t, task.StatusDeleted, tk.Status)
	require.Equal(t, "obsolete", tk.Error)
	require.False(t, tk.SetStatus(task.StatusDeleted, now, "ignored"))
	require.True(t, tk.SetStatus(task.StatusTodo, now, "retry"))
	require.Equal(t, task.StatusTodo, tk.Status)
	require.Empty(t, tk.Error)
	require.False(t, tk.SetStatus(task.StatusTodo, now, "still todo"))
	require.True(t, tk.SetStatus(task.StatusDone, now, "done"))
	require.Empty(t, tk.Error)
	require.True(t, tk.SetStatus(task.StatusFailed, now, "boom"))
	require.Equal(t, "boom", tk.Error)
	require.True(t, tk.SetStatus(task.StatusDoing, now, "retry"))
	require.Empty(t, tk.Error)
	require.Empty(t, tk.Finished)
	require.False(t, tk.SetStatus("not-real", now, ""))
}

func TestStatusMetaCoversAllOrderedStatuses(t *testing.T) {
	t.Parallel()

	now := time.Now()
	for _, status := range task.OrderedStatuses() {
		// Every canonical status must resolve to an event without panicking.
		require.NotEmpty(t, string(task.EventForStatus(status)),
			"EventForStatus(%q) returned empty event", status)

		// And must drive a transition through SetStatus from a fresh todo task.
		tk := task.Task{Status: task.StatusTodo}
		_ = tk.SetStatus(status, now, "msg")
		require.Equal(t, status, tk.Status,
			"SetStatus(%q) did not land on the target status", status)
	}
}

func TestEventForStatusPanicsOnUnknown(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"task: EventForStatus called with unknown status bogus — callers must validate via ParseStatus first",
		func() { _ = task.EventForStatus(task.Status("bogus")) })
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
		AvailableAt: "2026-07-17T16:00:00Z",
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
	require.Equal(t, tk.AvailableAt, opts.AvailableAt)
}

func TestRetryDispositionDefaultsToManual(t *testing.T) {
	t.Parallel()
	got, err := task.ParseRetryDisposition("")
	require.NoError(t, err)
	require.Equal(t, task.RetryDispositionManual, got)
	got, err = task.ParseRetryDisposition("deferred")
	require.NoError(t, err)
	require.Equal(t, task.RetryDispositionDeferred, got)
	_, err = task.ParseRetryDisposition("automatic")
	require.ErrorIs(t, err, task.ErrInvalidRetryDisposition)
	canonical, err := task.ValidateRetryDisposition(task.RetryDispositionDeferred, "2026-07-17T12:00:00-04:00")
	require.NoError(t, err)
	require.Equal(t, "2026-07-17T16:00:00Z", canonical)
	_, err = task.ValidateRetryDisposition(task.RetryDispositionDeferred, "")
	require.ErrorIs(t, err, task.ErrDeferredRetryRequiresAvailableAt)
	_, err = task.ValidateRetryDisposition(task.RetryDispositionManual, "2026-07-17T16:00:00Z")
	require.ErrorIs(t, err, task.ErrManualRetryWithAvailableAt)
}

func TestAllStatusesMatchesOrderedStatuses(t *testing.T) {
	t.Parallel()

	require.Equal(t, task.OrderedStatuses(), task.AllStatuses(),
		"AllStatuses must enumerate the same canonical statuses as OrderedStatuses")
}

func TestAllStatusesAllHaveStatusMetaEntries(t *testing.T) {
	t.Parallel()

	// Mirrors the init() startup guard: every enumerated status resolves to a
	// non-empty event via the statusMeta table without panicking.
	for _, status := range task.AllStatuses() {
		require.NotEmpty(t, string(task.EventForStatus(status)),
			"EventForStatus(%q) returned empty event — missing statusMeta entry", status)
	}
}

func TestBudgetLimitedStatus(t *testing.T) {
	t.Parallel()

	got, ok := task.ParseStatus("budget-limited")
	require.True(t, ok)
	require.Equal(t, task.StatusBudgetLimited, got)

	require.Equal(t, task.StatusBudgetLimited, task.NormalizeStatus("budget-limited"))
	require.True(t, task.ValidStatus(task.StatusBudgetLimited))

	// Suspended tasks stay visible to the user but are not claimable.
	require.True(t, task.VisibleStatus(task.StatusBudgetLimited))
	require.False(t, task.ActiveStatus(task.StatusBudgetLimited))

	require.Contains(t, task.OrderedStatuses(), task.StatusBudgetLimited)
}
