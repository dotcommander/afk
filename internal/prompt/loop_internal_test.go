package prompt

import (
	"strings"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestJoinCmdAndTaskDefaults(t *testing.T) {
	t.Parallel()

	require.Equal(t, "afk", joinCmd("afk", ""))
	require.Equal(t, "afk take", joinCmd("afk", "take"))

	out := Task("", task.Task{ID: "1", Status: task.StatusPending, Body: "do focused work"}, nil, nil)
	require.Contains(t, out, "afk set 1 done")
	require.Contains(t, out, "afk set 1 failed")
	require.NotContains(t, out, "History")
}

func TestPromptLimitHelpersAtBoundary(t *testing.T) {
	t.Parallel()

	events := make([]task.Event, maxPromptHistoryItems)
	attempts := make([]task.Attempt, maxPromptHistoryItems)
	gotEvents, omittedEvents := limitPromptEvents(events)
	gotAttempts, omittedAttempts := limitPromptAttempts(attempts)
	require.Len(t, gotEvents, maxPromptHistoryItems)
	require.Zero(t, omittedEvents)
	require.Len(t, gotAttempts, maxPromptHistoryItems)
	require.Zero(t, omittedAttempts)

	require.Equal(t, "abc", truncatePrompt("abc", 3))
	require.Equal(t, "界…", truncatePrompt(strings.Repeat("界", 3), 1))
}

func TestTaskIncludesAllMetadata(t *testing.T) {
	t.Parallel()

	out := Task("afk", task.Task{
		ID:          "1",
		Status:      task.StatusPending,
		Body:        "body",
		Priority:    "urgent",
		Tags:        []string{"repo:afk"},
		CWD:         "/tmp/repo",
		Source:      "cli",
		Agent:       "codex",
		GroupID:     "group",
		ResourceKey: "repo:/tmp/repo",
	}, nil, nil)
	require.Contains(t, out, "Priority")
	require.Contains(t, out, "repo:afk")
	require.Contains(t, out, "repo:/tmp/repo")
}
