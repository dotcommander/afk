package prompt_test

import (
	"strings"
	"testing"

	"github.com/dotcommander/afk/internal/prompt"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestLoopIncludesCurrentQueueInstructions(t *testing.T) {
	t.Parallel()

	out := prompt.Loop(prompt.LoopOptions{
		ExecutablePath: "/tmp/afk",
		SQLitePath:     "/tmp/tasks.sqlite",
		JSONLPath:      "/tmp/tasks.jsonl",
	})

	require.Contains(t, out, "/tmp/afk pop")
	require.Contains(t, out, "afk pop")
	require.Contains(t, out, "afk done <id>")
	require.Contains(t, out, "afk fail <id>")
	require.Contains(t, out, "/tmp/tasks.sqlite")
	require.Contains(t, out, "record `id`, `body`, and any metadata")
	require.Contains(t, out, "If the task has `cwd`, treat it as the likely working directory")
	require.Contains(t, out, "Do not read, write, patch, edit, or repair the queue database directly")
	require.Contains(t, out, "Do not pick up another task this tick")
	require.Contains(t, out, "/tmp/tasks.jsonl")
	require.Contains(t, out, "writes to a sibling `.sqlite` database")
}

func TestLoopFallsBackToAfkExecutable(t *testing.T) {
	t.Parallel()

	out := prompt.Loop(prompt.LoopOptions{})

	require.True(t, strings.Contains(out, "```bash\nafk pop\n```"))
	require.Contains(t, out, "SQLite-backed")
}

func TestTaskPromptIncludesContextHistoryAndFinalization(t *testing.T) {
	t.Parallel()

	out := prompt.Task("/tmp/afk", task.Task{
		ID:       "123",
		Status:   task.StatusFailed,
		Body:     "fix the thing",
		CWD:      "/tmp/repo",
		Priority: "high",
		Tags:     []string{"repo:afk"},
	}, []task.Event{{At: "2025-01-02T03:04:05Z", Type: task.EventFailed, Message: "boom"}}, []task.Attempt{{ID: 1, Status: task.StatusFailed, Started: "2025-01-02T03:00:00Z", Finished: "2025-01-02T03:04:05Z", Error: "boom"}})

	require.Contains(t, out, "AFK Task 123")
	require.Contains(t, out, "fix the thing")
	require.Contains(t, out, "/tmp/repo")
	require.Contains(t, out, "/tmp/afk done 123")
	require.Contains(t, out, "/tmp/afk fail 123")
	require.Contains(t, out, "attempt #1")
}
