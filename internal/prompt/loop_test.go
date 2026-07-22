package prompt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/afk/internal/prompt"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestLoopOutput_ExeIsBareName(t *testing.T) {
	t.Parallel()

	out := prompt.Loop(prompt.LoopOptions{ExecutablePath: "afk"})

	require.Contains(t, out, "afk take")
	require.NotContains(t, out, "/Users/")
	require.NotContains(t, out, "/home/")
	require.Contains(t, out, "Loop Tick - Process One Ready Task")
	require.Contains(t, out, "claims the first ready task")
	require.Contains(t, out, "No ready tasks.")
	require.NotContains(t, out, "first todo task")
	require.NotContains(t, out, "No todo tasks.")
	// Every bash block command must start with the bare name, not a slash-prefixed path.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "```") || line == "" {
			continue
		}
		// Lines immediately following ```bash open the block; check they don't start with /
		// We verify the positive form: any occurrence of "```bash\n/" would be wrong.
	}
	require.NotContains(t, out, "```bash\n/")
}

func TestLoopIncludesCurrentQueueInstructions(t *testing.T) {
	t.Parallel()

	out := prompt.Loop(prompt.LoopOptions{
		ExecutablePath: "/tmp/afk",
		SQLitePath:     "/tmp/tasks.sqlite",
	})

	require.Contains(t, out, "/tmp/afk take")
	require.Contains(t, out, "afk take")
	require.Contains(t, out, "afk set <id> done")
	require.Contains(t, out, "afk set <id> failed")
	require.Contains(t, out, `afk set <id> done --note "<verification evidence>" --worker <name> --summary`)
	require.Contains(t, out, `afk set <id> failed --note "<one-line reason>" --worker <name> --summary`)
	require.Contains(t, out, `afk set <id> failed --note "orphaned doing claim" --force`)
	require.Contains(t, out, `afk retry <id> --reason`)
	require.Contains(t, out, "/tmp/tasks.sqlite")
	require.Contains(t, out, "record `id`, `body`, and any metadata")
	require.Contains(t, out, "If the task has `cwd`, treat it as the likely working directory")
	require.Contains(t, out, "Do not read, write, patch, edit, or repair the queue database directly")
	require.Contains(t, out, "Do not pick up another task this tick")
	require.NotContains(t, out, "tasks.jsonl")
	require.NotContains(t, out, "Migration Note")
}

func TestLoopIncludesPollingDiscipline(t *testing.T) {
	t.Parallel()

	out := prompt.Loop(prompt.LoopOptions{})

	require.Contains(t, out, "Minimum interval >= 60s.")
	require.Contains(t, out, "Summary-only poller, gated on change.")
	require.Contains(t, out, "Full JSON only on state change.")
	require.Contains(t, out, "No timer loops.")
}

func TestLoopFallsBackToAfkExecutable(t *testing.T) {
	t.Parallel()

	out := prompt.Loop(prompt.LoopOptions{})

	require.True(t, strings.Contains(out, "```bash\nafk take --worker <name> --lease 60m --summary\n```"))
	require.Contains(t, out, "afk take --dry-run --summary --full --envelope --limit 5")
	require.Contains(t, out, "SQLite-backed")
}

func TestLoopOutput_QueuePathTilde(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir:", err)
	}

	absPath := filepath.Join(home, ".claude/queue/tasks.sqlite")
	out := prompt.Loop(prompt.LoopOptions{SQLitePath: absPath})

	require.Contains(t, out, "~/.claude/queue/tasks.sqlite")
	require.NotContains(t, out, absPath)
}

func TestLoopOutput_QueuePathCustom(t *testing.T) {
	t.Parallel()

	out := prompt.Loop(prompt.LoopOptions{SQLitePath: "/tmp/test.sqlite"})

	require.Contains(t, out, "/tmp/test.sqlite")
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
	}, []task.Event{{At: "2025-01-02T03:04:05Z", Type: task.EventFailed, Message: "boom"}}, []task.Attempt{{ID: 1, Status: task.StatusFailed, Started: "2025-01-02T03:00:00Z", Finished: "2025-01-02T03:04:05Z", Error: "boom"}}, nil)

	require.Contains(t, out, "AFK Task 123")
	require.Contains(t, out, "fix the thing")
	require.Contains(t, out, "/tmp/repo")
	require.Contains(t, out, "/tmp/afk set 123 done")
	require.Contains(t, out, "/tmp/afk set 123 failed")
	require.Contains(t, out, `/tmp/afk retry 123 --reason`)
	require.Contains(t, out, "attempt #1")

	// Regression: body must be wrapped in XML delimiters (prompt-injection hardening).
	// Verify the body appears *between* the tags, not just that all three substrings
	// exist independently somewhere in the output.
	openIdx := strings.Index(out, "<task-body>")
	closeIdx := strings.Index(out, "</task-body>")
	require.NotEqual(t, -1, openIdx, "output must contain <task-body> opening tag")
	require.NotEqual(t, -1, closeIdx, "output must contain </task-body> closing tag")
	require.Less(t, openIdx, closeIdx, "<task-body> must precede </task-body>")
	between := out[openIdx+len("<task-body>") : closeIdx]
	require.Contains(t, between, "fix the thing", "task body must appear inside <task-body>...</task-body>")
}

func TestTaskPromptBoundsBodyAndHistory(t *testing.T) {
	t.Parallel()

	events := make([]task.Event, 51)
	attempts := make([]task.Attempt, 51)
	for i := range events {
		events[i] = task.Event{At: "2025-01-02T03:04:05Z", Type: task.EventFailed, Message: strings.Repeat("e", 1100)}
		attempts[i] = task.Attempt{ID: int64(i + 1), Status: task.StatusFailed, Started: "2025-01-02T03:04:05Z", Error: strings.Repeat("a", 1100)}
	}

	out := prompt.Task("afk", task.Task{ID: "123", Status: task.StatusTodo, Body: strings.Repeat("b", 9000)}, events, attempts, nil)

	require.Contains(t, out, "older events omitted by output limit")
	require.Contains(t, out, "older attempts omitted by output limit")
	require.NotContains(t, out, strings.Repeat("b", 8500))
	require.NotContains(t, out, strings.Repeat("e", 1050))
	require.NotContains(t, out, strings.Repeat("a", 1050))
}
