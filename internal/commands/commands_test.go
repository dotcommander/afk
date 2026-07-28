package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

func TestCommandsLifecycleThroughRoot(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "hello", "world"))
	require.NotEmpty(t, id)
	require.Contains(t, run("tasks"), "hello world")
	require.Contains(t, run("tasks", "--status", "todo", "--json"), id)
	require.Contains(t, run("find", "hello", "--json"), id)

	showJSON := run("task", id, "--json")
	shown := taskDetailJSON(t, showJSON)
	require.Equal(t, id, shown["id"])
	require.Equal(t, "todo", shown["status"])

	require.Contains(t, run("take", "--dry-run", "--limit", "1", "--json"), id)
	takeOut := run("take", "--lease", "30m", "--worker", "worker-1")
	require.Contains(t, takeOut, id)
	require.Contains(t, takeOut, `"status":"doing"`)
	require.Contains(t, takeOut, `"lease_expires"`)
	require.Contains(t, run("task", id), "worker=worker-1")

	require.Equal(t, "set "+id+" done\n", run("set", id, "done", "--force"))
	require.Contains(t, run("status"), "done: 1")
	require.NotContains(t, run("tasks", "--status", "todo"), id)

	failedID := strings.TrimSpace(run("add", "--no-cwd", "fail-me"))
	require.JSONEq(t, `{"id":"`+failedID+`","status":"failed","title":"fail-me","note":"oops"}`, run("set", failedID, "failed", "oops", "--json"))
	require.Contains(t, run("task", failedID), "Error: oops")

	require.Equal(t, "set "+failedID+" deleted\n", run("set", failedID, "deleted", "cleanup"))
	require.NotContains(t, run("tasks"), failedID)
	require.Contains(t, run("tasks", "--status", "deleted"), failedID)
}

func TestAddAvailableAtIsPersistedAndDefersTake(t *testing.T) {
	t.Parallel()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "--available-at", "2099-01-01T00:00:00Z", "future task"))
	require.Contains(t, run("task", id, "--json"), `"available_at":"2099-01-01T00:00:00Z"`)
	require.NotContains(t, run("take", "--dry-run", "--json"), id)
}

func TestFormatVybeImportReportIsCompleteAndDeterministic(t *testing.T) {
	t.Parallel()
	report := store.VybeImportReport{
		SourceSHA256:        "abc123",
		CutoverID:           "cutover-1",
		DryRun:              true,
		AlreadyImported:     false,
		SourceRows:          map[string]int64{"tasks": 3, "artifacts": 1},
		ImportedTasks:       2,
		ImportedEvents:      4,
		ImportedCheckpoints: 5,
		ImportedArtifacts:   1,
		ArchivedOnly:        map[string]int64{"projects": 2, "events": 1},
		ArchivedOrphans:     map[string]int64{"events": 1},
	}
	require.Equal(t,
		"vybe import source_sha256=abc123 cutover_id=cutover-1 mode=dry-run replay=false source_rows={artifacts=1,tasks=3} imported={artifacts=1,checkpoints=5,events=4,tasks=2} archived_only={agent_state=0,artifacts=0,events=1,idempotency=0,memory=0,projects=2} archived_orphans={artifacts=0,events=1,memory=0}",
		formatVybeImportReport(report),
	)
}

func TestTakeDryRunFullAndEnvelope(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	body := strings.Repeat("x", 700)
	id := strings.TrimSpace(run("add", "--no-cwd", body))

	truncated := run("take", "--dry-run", "--limit", "1", "--json")
	require.Contains(t, truncated, id)
	require.Contains(t, truncated, `"body_truncated":true`)
	require.Contains(t, truncated, `"body_hint":"use --full to see the complete task body"`)

	var summaryDoc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("take", "--dry-run", "--limit", "1", "--summary"))), &summaryDoc))
	require.Equal(t, false, summaryDoc["claimed"])
	require.NotNil(t, summaryDoc["queue"])
	summaryTasks := summaryDoc["tasks"].([]any)
	require.Len(t, summaryTasks, 1)
	summaryTask := summaryTasks[0].(map[string]any)
	require.Equal(t, id, summaryTask["id"])
	require.Equal(t, true, summaryTask["body_truncated"])
	require.Equal(t, "use --full to see the complete task body", summaryTask["body_hint"])

	full := run("take", "--dry-run", "--limit", "1", "--json", "--full")
	require.Contains(t, full, id)
	require.NotContains(t, full, "body_truncated")
	require.NotContains(t, full, "body_hint")
	require.Contains(t, full, body)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("take", "--dry-run", "--limit", "1", "--full", "--envelope"))), &doc))
	require.Equal(t, false, doc["claimed"])
	require.NotNil(t, doc["queue"])
	tasks := doc["tasks"].([]any)
	require.Len(t, tasks, 1)
	taskDoc := tasks[0].(map[string]any)
	require.Equal(t, id, taskDoc["id"])
	require.Equal(t, body, taskDoc["body"])
}

func TestTakeHelpIncludesAgentLoop(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := newTestCLI(d, "test")
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs([]string{"take", "--help"})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	out := stdout.String()
	require.Contains(t, out, "Agent loop:")
	require.Contains(t, out, "afk take --worker <name> --lease 60m --summary")
	require.Contains(t, out, `afk set <id> done --note "<verification>"`)
	require.Contains(t, out, "body_truncated=true")
	require.Contains(t, out, "emit JSONL output; enabled by default")
}

func TestTakeDryRunLimitZeroReturnsAllReadyTasks(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	firstID := strings.TrimSpace(run("add", "--no-cwd", "first ready task"))
	secondID := strings.TrimSpace(run("add", "--no-cwd", "second ready task"))

	ready := run("take", "--dry-run", "--limit", "0", "--json")
	require.Contains(t, ready, firstID)
	require.Contains(t, ready, secondID)
}

func TestTakeExplainsNoReadyTasksBlockedByResourceLock(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	require.NoError(t, run("add", "--no-cwd", "--resource", "repo:one", "first ready task"))
	require.NoError(t, run("take", "--worker", "worker-1", "--lease", "30m"))
	require.NoError(t, run("add", "--no-cwd", "--resource", "repo:one", "resource locked task"))

	require.NoError(t, run("take"))
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "No ready tasks")
	require.Contains(t, stderr.String(), "1 of 1 todo task(s) blocked by dependencies, resource locks, or unsatisfied gates.")
}

func TestTakeSummaryJSON(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	firstID := strings.TrimSpace(run("add", "--no-cwd", "first task"))
	secondID := strings.TrimSpace(run("add", "--no-cwd", "second task"))
	doneID := strings.TrimSpace(run("add", "--no-cwd", "done task"))
	run("set", doneID, "done", "--force")

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("take", "--summary", "--lease", "30m"))), &doc))
	claimed, ok := doc["task"].(map[string]any)
	require.True(t, ok, "take --summary must include task object")
	require.Contains(t, []string{firstID, secondID}, claimed["id"])
	require.Equal(t, "doing", claimed["status"])
	require.NotEmpty(t, claimed["lease_expires"])

	queue, ok := doc["queue"].(map[string]any)
	require.True(t, ok, "take --summary must include queue object")
	require.Equal(t, float64(1), queue["todo"])
	require.Equal(t, float64(1), queue["doing"])
	require.Equal(t, float64(1), queue["done"])
	require.Equal(t, float64(0), queue["failed"])
	require.Equal(t, float64(0), queue["deleted"])
	require.Equal(t, float64(3), queue["total"])
	require.Equal(t, float64(1), queue["ready_remaining"])
}

func TestTakeUsesCurrentCommandNameInWriteErrors(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	stdout := &bytes.Buffer{}

	run := func(args ...string) error {
		t.Helper()
		d.Stdout = stdout
		stdout.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	require.NoError(t, run("add", "--no-cwd", "take write error task"))
	d.Stdout = commandFailWriter{}
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "take"})
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "take: write")
	require.NotContains(t, err.Error(), "pop")
}

func TestTakeRejectsInvalidClaimWithCurrentCommandName(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	t.Setenv("AFK_ALLOW_FORCE", "1")
	require.NoError(t, run("add", "--force", "--no-cwd", "pick my nose"))
	err := run("take")
	require.Error(t, err)
	require.Contains(t, err.Error(), "take ")
	require.NotContains(t, err.Error(), "pop")
}

func TestSetNoteFlagsAndSummary(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "task title. second sentence"))
	require.JSONEq(t, `{"id":"`+id+`","status":"failed","title":"task title","note":"go test ./... && echo ok","queue":{"todo":0,"doing":0,"done":0,"failed":1,"deleted":0,"total":1}}`, run("set", id, "failed", "--note", "go test ./... && echo ok", "--summary"))

	notePath := filepath.Join(t.TempDir(), "note.txt")
	require.NoError(t, os.WriteFile(notePath, []byte("ready again\n"), 0o600))
	require.JSONEq(t, `{"id":"`+id+`","status":"todo","title":"task title","note":"ready again"}`, run("set", id, "todo", "--note-file", notePath, "--json"))

	d.Stdin = strings.NewReader("verified with quotes \"ok\" && done\n")
	require.JSONEq(t, `{"id":"`+id+`","status":"done","title":"task title","note":"verified with quotes \"ok\" && done"}`, run("set", id, "done", "--note-file", "-", "--json"))
}

func TestSetDoingCreatesRetryAttempt(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "retry task"))
	run("take", "--worker", "retry-worker")
	run("set", id, "failed", "blocked", "--worker", "retry-worker")
	run("set", id, "doing", "retrying")
	run("set", id, "done", "--force")

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("task", id, "--json"))), &doc))
	taskDoc := doc["task"].(map[string]any)
	require.Equal(t, "done", taskDoc["status"])
	require.NotContains(t, taskDoc, "error")
	attempts := doc["attempts"].([]any)
	require.Len(t, attempts, 2)
	require.Contains(t, fmt.Sprint(attempts[0]), "failed")
	require.Contains(t, fmt.Sprint(attempts[1]), "done")
}

func TestSetTerminalRequiresCompletionNote(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	exec := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	addID := func() string {
		t.Helper()
		require.NoError(t, exec("add", "--no-cwd", "completion-note task"))
		return strings.TrimSpace(stdout.String())
	}

	// done without a note is rejected.
	id := addID()
	err := exec("set", id, "done")
	require.ErrorIs(t, err, task.ErrMissingCompletionNote)
	require.Equal(t, "todo", currentStatus(t, d, queuePath, id))

	// failed without a note is rejected.
	failID := addID()
	err = exec("set", failID, "failed")
	require.ErrorIs(t, err, task.ErrMissingCompletionNote)
	require.Equal(t, "todo", currentStatus(t, d, queuePath, failID))

	// --force bypasses the guard.
	forceID := addID()
	require.NoError(t, exec("set", forceID, "done", "--force"))
	require.Equal(t, "done", currentStatus(t, d, queuePath, forceID))

	// a note satisfies the guard.
	noteID := addID()
	require.NoError(t, exec("set", noteID, "done", "--note", "verified: go test ./... passed"))
	require.Equal(t, "done", currentStatus(t, d, queuePath, noteID))
}

func TestSetForcePreservesWorkerFenceAndReceipt(t *testing.T) {
	t.Parallel()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	exec := func(args ...string) error {
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}
	require.NoError(t, exec("add", "--no-cwd", "worker task"))
	id := strings.TrimSpace(stdout.String())
	require.NoError(t, exec("take", "--worker", "owner", "--lease", "30m"))
	require.ErrorIs(t, exec("set", id, "todo", "--worker", "stale", "--json"), store.ErrWorkerMismatch)
	require.ErrorIs(t, exec("set", id, "done", "--force", "--worker", "stale", "--json"), store.ErrWorkerMismatch)
	require.NoError(t, exec("set", id, "done", "--force", "--worker", "owner", "--json"))
	require.JSONEq(t, `{"id":"`+id+`","status":"done","title":"worker task"}`, stdout.String())
}

func TestSetRequiresWorkerForOwnedTerminalAttempt(t *testing.T) {
	t.Parallel()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	exec := func(args ...string) error {
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}
	require.NoError(t, exec("add", "--no-cwd", "owned task"))
	id := strings.TrimSpace(stdout.String())
	require.NoError(t, exec("take", "--worker", "owner", "--lease", "30m"))

	require.ErrorIs(t, exec("set", id, "done", "--note", "late", "--json"), store.ErrWorkerMismatch)
	require.ErrorIs(t, exec("set", id, "failed", "--note", "late", "--request-id", "req-1", "--json"), store.ErrWorkerMismatch)
	require.Equal(t, "doing", currentStatus(t, d, queuePath, id))

	require.NoError(t, exec("set", id, "done", "--force", "--json"))
	require.Equal(t, "done", currentStatus(t, d, queuePath, id))
}

func currentStatus(t *testing.T, d *Deps, queuePath, id string) string {
	t.Helper()
	buf := &bytes.Buffer{}
	root := newTestCLI(d, "test")
	saved := d.Stdout
	d.Stdout = buf
	defer func() { d.Stdout = saved }()
	root.SetArgs([]string{"--queue", queuePath, "task", id, "--json"})
	require.NoError(t, root.Execute())
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &doc))
	return doc["task"].(map[string]any)["status"].(string)
}

func TestRetryCommandCreatesRetryAttempt(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "retry command task"))
	run("take", "--worker", "retry-worker")
	run("set", id, "failed", "workspace permission blocked", "--worker", "retry-worker")

	require.JSONEq(t, `{"id":"`+id+`","status":"doing","note":"retrying: workspace permission approved","disposition":"manual"}`, run("retry", id, "--reason", "workspace permission approved", "--json"))
	run("set", id, "done", "--force")

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("task", id, "--json"))), &doc))
	taskDoc := doc["task"].(map[string]any)
	require.Equal(t, "done", taskDoc["status"])
	require.NotContains(t, taskDoc, "error")
	attempts := doc["attempts"].([]any)
	require.Len(t, attempts, 2)
	require.Contains(t, fmt.Sprint(attempts[0]), "workspace permission blocked")
	require.Contains(t, fmt.Sprint(attempts[1]), "done")
}

func TestRetryCommandDefaultReason(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "retry default task"))
	require.Equal(t, "retry "+id+" doing\n", run("retry", id))
	require.Contains(t, run("task", id), "retrying")
}

func TestRetryCommandDeferredDispositionReturnsTaskToFutureTodo(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "deferred retry task"))
	run("take", "--worker", "retry-worker")
	run("set", id, "failed", "temporary blocker", "--worker", "retry-worker")
	availableAt := "2099-01-01T00:00:00Z"
	require.JSONEq(t, `{"id":"`+id+`","status":"todo","note":"retry deferred until `+availableAt+`: wait for window","disposition":"deferred","available_at":"`+availableAt+`"}`,
		run("retry", id, "--disposition", "deferred", "--available-at", availableAt, "--reason", "wait for window", "--json"))
	require.NotContains(t, run("take", "--dry-run", "--json"), id)
}

func TestSnapshotCommandJSONAndOutputFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	firstID := strings.TrimSpace(run("add", "--no-cwd", "first task"))
	secondID := strings.TrimSpace(run("add", "--no-cwd", "second task"))
	run("take", "--worker", "worker-1")

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("snapshot", "--label", "after", "--task", firstID))), &doc))
	require.Equal(t, "after", doc["label"])
	require.Equal(t, "2025-01-02T03:04:05Z", doc["created"])

	counts, ok := doc["counts"].(map[string]any)
	require.True(t, ok, "snapshot must include counts")
	require.Equal(t, float64(1), counts["todo"])
	require.Equal(t, float64(1), counts["doing"])
	require.Equal(t, float64(2), counts["total"])
	require.Equal(t, float64(1), counts["ready"])
	health, ok := doc["health"].(map[string]any)
	require.True(t, ok, "snapshot must include queue health")
	require.Equal(t, float64(86400), health["window_seconds"])
	duration, ok := health["terminal_attempt_duration_seconds"].(map[string]any)
	require.True(t, ok, "snapshot health must include terminal attempt duration")
	require.Equal(t, float64(0), duration["count"])
	require.Nil(t, duration["avg"])
	require.Nil(t, duration["p50"])
	require.Nil(t, duration["p90"])

	tasks, ok := doc["tasks"].(map[string]any)
	require.True(t, ok, "snapshot must include task lists")
	ready, ok := tasks["ready"].([]any)
	require.True(t, ok, "snapshot must include ready list")
	require.Len(t, ready, 1)
	require.Contains(t, fmt.Sprint(ready[0]), secondID)

	detail, ok := doc["task"].(map[string]any)
	require.True(t, ok, "snapshot --task must include task detail")
	taskDoc, ok := detail["task"].(map[string]any)
	require.True(t, ok, "snapshot task detail must include task")
	require.Equal(t, firstID, taskDoc["id"])

	outPath := filepath.Join(dir, "snapshot.json")
	require.Empty(t, run("snapshot", "--output", outPath))
	outBytes, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(outBytes, &doc))
}

func TestStatusSummaryJSON(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	todoID := strings.TrimSpace(run("add", "--no-cwd", "todo task"))
	doneID := strings.TrimSpace(run("add", "--no-cwd", "done task"))
	run("set", doneID, "done", "--force")
	require.Contains(t, run("take"), todoID)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("status", "--summary", "--json"))), &doc))
	require.Equal(t, float64(0), doc["todo"])
	require.Equal(t, float64(1), doc["doing"])
	require.Equal(t, float64(1), doc["done"])
	require.Equal(t, float64(0), doc["failed"])
	require.Equal(t, float64(0), doc["deleted"])
	require.Len(t, doc, 5, "summary wire shape must remain counts-only")
}

func TestStatusShowsClaimDiagnostics(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	d := testDepsWithWriters(stdout, stderr)
	d.Now = func() time.Time { return now }

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "leased task"))
	require.Contains(t, run("take", "--lease", "30m"), id)
	now = now.Add(31 * time.Minute)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("status", "--json"))), &doc))
	tasks := doc["tasks"].(map[string]any)
	doing := tasks["doing"].([]any)
	require.Len(t, doing, 1)
	doingTask := doing[0].(map[string]any)
	claim := doingTask["claim"].(map[string]any)
	require.Equal(t, float64(31*60), claim["age_seconds"])
	require.Equal(t, true, claim["stale"])
	require.Equal(t, "lease_expired", claim["reason"])

	text := run("status")
	require.Contains(t, text, "age=31m0s")
	require.Contains(t, text, "stale=lease_expired")
}

func TestStatusBlockedExplainsDependencyBlockers(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	prereqID := strings.TrimSpace(run("add", "--no-cwd", "prereq task"))
	blockedID := strings.TrimSpace(run("add", "--no-cwd", "--blocked-by", prereqID, "blocked task"))

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("status", "--blocked", "--json"))), &doc))
	blocked := doc["blocked"].([]any)
	require.Len(t, blocked, 1)
	blockedRow := blocked[0].(map[string]any)
	taskDoc := blockedRow["task"].(map[string]any)
	require.Equal(t, blockedID, taskDoc["id"])
	blockers := blockedRow["blockers"].([]any)
	require.Len(t, blockers, 1)
	blocker := blockers[0].(map[string]any)
	require.Equal(t, prereqID, blocker["id"])
	require.Equal(t, "todo", blocker["status"])

	text := run("status", "--blocked")
	require.Contains(t, text, "Blocked:")
	require.Contains(t, text, blockedID)
	require.Contains(t, text, prereqID+"(todo)")

	run("set", prereqID, "done", "--force")
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("status", "--blocked", "--json"))), &doc))
	require.Empty(t, doc["blocked"])
}

func TestOldCommandsAreNotPublic(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ls", "pop", "done", "fail", "explain", "run", "prune", "doctor"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newTestCLI(testDepsWithWriters(stdout, stderr), "test")
			root.SetArgs([]string{"--queue", filepath.Join(t.TempDir(), "tasks.sqlite"), name})
			err := root.Execute()
			require.Error(t, err)
			require.Contains(t, err.Error(), "unknown command")
		})
	}
}

func TestStatusCommandEmptyQueue(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newTestCLI(testDepsWithWriters(stdout, stderr), "test")
	root.SetArgs([]string{"--queue", filepath.Join(t.TempDir(), "tasks.sqlite"), "status"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	out := stdout.String()
	require.Contains(t, out, "todo: 0")
	require.Contains(t, out, "doing: 0")
	require.Contains(t, out, "Todo:")
	require.Contains(t, out, "Doing:")
}

func TestResolveQueuePathsUsesFlagEnvAndDefault(t *testing.T) {
	flagPath := filepath.Join(t.TempDir(), "queue.jsonl")
	paths, err := resolveQueuePaths(flagPath)
	require.NoError(t, err)
	require.Equal(t, strings.TrimSuffix(flagPath, ".jsonl")+".sqlite", paths.SQLitePath)

	envPath := filepath.Join(t.TempDir(), "env.sqlite")
	t.Setenv("AFK_QUEUE", envPath)
	paths, err = resolveQueuePaths("")
	require.NoError(t, err)
	require.Equal(t, envPath, paths.SQLitePath)

	t.Setenv("AFK_QUEUE", "")
	paths, err = resolveQueuePaths("")
	require.NoError(t, err)
	require.NotEmpty(t, paths.SQLitePath)
}

func TestCommandVariantsAndErrorPaths(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	require.NoError(t, run("add", "--no-cwd", "variant task"))
	id := strings.TrimSpace(stdout.String())

	require.NoError(t, run("status", "--summary"))
	require.Contains(t, stdout.String(), "todo: 1")
	require.NotContains(t, stdout.String(), "Todo:")

	require.NoError(t, run("find", "variant"))
	require.Contains(t, stdout.String(), id)

	require.NoError(t, run("prompt", "--task", id))
	require.Contains(t, stdout.String(), "AFK Task "+id)

	outPath := filepath.Join(t.TempDir(), "discover.md")
	require.NoError(t, run("prompt", "--discover", "--output", outPath))
	require.Empty(t, stdout.String())
	body, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Contains(t, string(body), "task-discovery contract")
	require.Contains(t, string(body), "## Happy path")

	err = run("tasks", "--status", "bogus")
	require.ErrorIs(t, err, task.ErrInvalidStatus)
	err = run("set", id, "bogus")
	require.ErrorIs(t, err, task.ErrInvalidStatus)
	err = run("task", "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestAddDryRunDiagnoseForceAndDefaults(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	repo := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o755))
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	require.NoError(t, run("add", "--dry-run", "--json", "--cwd", repo, "valid task body"))
	require.JSONEq(t, `{"valid":true}`, strings.TrimSpace(stdout.String()))

	require.NoError(t, run("add", "--diagnose", "--no-cwd", "valid task body"))
	require.Contains(t, stdout.String(), "task validates")

	err := run("add", "--force", "--diagnose", "--no-cwd", "valid task body")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")

	err = run("add", "--diagnose", "--source", "task-discovery", "--cwd", repo, "too vague")
	require.Error(t, err)
	require.Contains(t, stderr.String(), "invalid task")

	err = run("add", "--force", "too vague")
	require.Error(t, err)
	require.Contains(t, err.Error(), "AFK_ALLOW_FORCE=1")

	require.NoError(t, run("add", "--no-cwd", "force prerequisite"))
	prereqID := strings.TrimSpace(stdout.String())

	t.Setenv("AFK_ALLOW_FORCE", "1")
	require.NoError(t, run("add", "--force", "--json", "--cwd", repo, "too vague"))
	require.Contains(t, stderr.String(), "warning: --force")
	var created map[string]string
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &created))
	require.NotEmpty(t, created["id"])

	require.NoError(t, run("add", "--force", "--json", "--no-cwd", "--blocked-by", prereqID, "too vague"))
	require.Contains(t, stderr.String(), "warning: --force")

	require.NoError(t, run("task", created["id"], "--json"))
	shown := taskDetailJSON(t, stdout.String())
	require.Equal(t, repo, shown["cwd"])
	require.Equal(t, "repo:"+repo, shown["resource_key"])
	require.Contains(t, shown["tags"], "repo:"+filepath.Base(repo))
}

func TestAddCommandOptionErrorsAndBlockedBy(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	require.NoError(t, run("add", "--no-cwd", "first"))
	first := strings.TrimSpace(stdout.String())
	require.NoError(t, run("add", "--no-cwd", "--blocked-by", "none", "independent"))
	require.NoError(t, run("add", "--no-cwd", "--blocked-by", first, "dependent"))
	dependent := strings.TrimSpace(stdout.String())
	require.NoError(t, run("task", dependent, "--json"))
	require.Contains(t, stdout.String(), first)

	err := run("add", "--no-cwd", "--blocked-by", "missing-id", "bad dependent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing-id")

	err = run("add", "--no-cwd", "--priority", "invalid", "bad priority")
	require.Error(t, err)
	require.Contains(t, err.Error(), "priority must be")
}

func TestAddCommandHelpers(t *testing.T) {
	t.Parallel()

	values := []string{"a", "", "b"}
	require.Equal(t, []string{"a", "b"}, nonEmpty(values))
	require.Equal(t, "", fieldIfSet("cwd", ""))
	require.Equal(t, "cwd=/tmp/repo", fieldIfSet("cwd", "/tmp/repo"))
	require.False(t, isTerminalWriter(&bytes.Buffer{}))
	require.True(t, task.IsGeneratedCandidate("task-discovery", nil))
	require.True(t, task.IsGeneratedCandidate("", []string{" discovery "}))
	require.False(t, task.IsGeneratedCandidate("", nil))

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	require.NoError(t, writeAddDryRunResult(d, false))
	require.Equal(t, "valid\n", stdout.String())

	stdout.Reset()
	require.NoError(t, writeAddDryRunResult(d, true))
	require.JSONEq(t, `{"valid":true}`, strings.TrimSpace(stdout.String()))

	err := addValidationError(context.Background(), task.AddOptions{Source: "task-discovery"}, task.ErrInvalidTask)
	require.Error(t, err)
	require.Contains(t, err.Error(), "generated/discovery tasks")
}

func TestCommandRunEPropagatesServiceErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	d.Service = app.NewService(&commandErrorStore{err: boom}, time.Now)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "tasks", args: []string{"tasks"}},
		{name: "find", args: []string{"find", "x"}},
		{name: "status", args: []string{"status"}},
		{name: "task", args: []string{"task", "id"}},
		{name: "set", args: []string{"set", "id", "done", "--force"}},
		{name: "take dry-run", args: []string{"take", "--dry-run"}},
		{name: "take claim", args: []string{"take"}},
		{name: "retry", args: []string{"retry", "id"}},
		{name: "snapshot", args: []string{"snapshot"}},
		{name: "requeue", args: []string{"requeue-stale", "--older-than", "1s"}},
		{name: "heartbeat", args: []string{"heartbeat", "id", "--worker", "worker"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := Execute(context.Background(), tc.args, d.Stdout, d.Stderr, d, "test")
			require.ErrorIs(t, err, boom)
		})
	}
}

func TestRunTakeClaimIncludesValidationContextWhenAutoFailFails(t *testing.T) {
	t.Parallel()

	boom := errors.New("update failed")
	claimed := task.Task{
		ID:      "forced-invalid",
		Created: "2025-01-02T03:04:05Z",
		Status:  task.StatusDoing,
		Body:    "pick my nose",
	}
	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	d.Service = app.NewService(&commandInvalidClaimStore{
		commandErrorStore: commandErrorStore{err: boom},
		claimed:           claimed,
	}, time.Now)

	err := runTakeClaim(context.Background(), d, 0, "worker-1", "", nil, false, false, false)
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), "forced-invalid")
	require.Contains(t, err.Error(), "invalid claimed task")
	require.Contains(t, err.Error(), "auto-fail")
	require.Contains(t, err.Error(), "invalid task")
}

func TestRunAddForcePropagatesDependencyError(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	d.Service = app.NewService(&commandDependencyErrorStore{}, time.Now)
	t.Setenv("AFK_ALLOW_FORCE", "1")

	err := runAddForce(context.Background(), d, task.AddOptions{Body: "forced"}, "dep", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency boom")
	require.Contains(t, stderr.String(), "warning: --force")
}

func TestRunAddForcePropagatesWarningWriteError(t *testing.T) {
	stdout := &bytes.Buffer{}
	d := testDepsWithWriters(stdout, &bytes.Buffer{})
	d.Stderr = commandFailWriter{}
	d.Service = app.NewService(&commandDependencyErrorStore{}, time.Now)
	t.Setenv("AFK_ALLOW_FORCE", "1")

	err := runAddForce(context.Background(), d, task.AddOptions{Body: "forced"}, "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write failed")
}

func TestRunAddNormalPropagatesDependencyError(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	st := &commandDependencyErrorStore{}
	d.Service = app.NewService(st, time.Now)

	err := runAddNormal(context.Background(), d, task.AddOptions{Body: "normal"}, "dep", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency boom")
	require.Empty(t, st.tasks)
}

func TestRunAddNormalPropagatesResultWriteError(t *testing.T) {
	t.Parallel()

	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	d.Stdout = commandFailWriter{}
	d.Service = app.NewService(&commandDependencyErrorStore{}, time.Now)

	err := runAddNormal(context.Background(), d, task.AddOptions{Body: "normal"}, "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write failed")
}

func TestRunAddDiagnosePropagatesWriterError(t *testing.T) {
	t.Parallel()

	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	d.Stderr = commandFailWriter{}
	err := runAddDiagnose(context.Background(), d, task.AddOptions{
		Body:   "",
		Source: "task-discovery",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "write failed")
}

func TestMaintenanceCommands(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	require.NoError(t, run("add", "--no-cwd", "stale task"))
	id := strings.TrimSpace(stdout.String())
	require.NoError(t, run("take", "--worker", "worker-1", "--lease", "1ms"))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, run("requeue-stale", "--older-than", "1ms"))
	require.Contains(t, stdout.String(), id)
	require.Contains(t, stderr.String(), "deprecated")

	require.NoError(t, run("take", "--worker", "worker-1", "--lease", "1ms"))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, run("reap", "--older-than", "1ms"))
	require.Contains(t, stdout.String(), id)
	require.Empty(t, stderr.String())

	require.NoError(t, run("take", "--worker", "worker-1", "--lease", "1m"))
	require.NoError(t, run("heartbeat", id, "--worker", "worker-1", "--lease", "2m"))
	require.Contains(t, stderr.String(), "deprecated")

	err := run("requeue-stale", "--older-than", "soon")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse older-than")

	err = run("requeue-stale", "--older-than", "0s")
	require.Error(t, err)
	require.Contains(t, err.Error(), "duration must be positive")
}

func TestParseOptionalDurationRejectsNonPositiveSuppliedValues(t *testing.T) {
	t.Parallel()

	dur, err := parseOptionalDuration("lease", "")
	require.NoError(t, err)
	require.Zero(t, dur)

	dur, err = parseOptionalDuration("lease", "1m")
	require.NoError(t, err)
	require.Equal(t, time.Minute, dur)

	for _, raw := range []string{"0s", "-1s"} {
		_, err := parseOptionalDuration("lease", raw)
		require.Error(t, err)
		require.Contains(t, err.Error(), "duration must be positive")
	}
}

func TestTakeRejectsInvalidClaimAndServeValidation(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := newTestCLI(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	t.Setenv("AFK_ALLOW_FORCE", "1")
	require.NoError(t, run("add", "--force", "--no-cwd", "pick my nose"))
	id := strings.TrimSpace(stdout.String())
	err := run("take")
	require.Error(t, err)
	require.Contains(t, err.Error(), id)
	require.Contains(t, err.Error(), "invalid task")

	err = run("serve", "--addr", "not-an-addr")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid addr")

	serveOut := &lockedBuffer{}
	serveErr := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	root := newTestCLI(&Deps{
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Stdout: serveOut,
		Stderr: serveErr,
		Now:    func() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) },
	}, "test")
	root.SetContext(ctx)
	root.SetArgs([]string{"--queue", queuePath, "serve", "--addr", "127.0.0.1:0", "--open=false"})
	done := make(chan error, 1)
	go func() {
		done <- root.Execute()
	}()
	require.Eventually(t, func() bool {
		return strings.Contains(serveOut.String(), "afk dashboard:")
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	require.Contains(t, serveOut.String(), "afk dashboard:")

	badStderrDeps := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	badStderrDeps.Service = d.Service
	badStderrDeps.Stderr = commandFailWriter{}
	err = Execute(context.Background(), []string{"--queue", queuePath, "serve", "--addr", "0.0.0.0:0"}, badStderrDeps.Stdout, badStderrDeps.Stderr, badStderrDeps, "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "write warning")
}

func taskDetailJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &doc))
	taskDoc, ok := doc["task"].(map[string]any)
	require.True(t, ok, "task JSON must include task object: %s", out)
	return taskDoc
}

func testDepsWithWriters(stdout, stderr *bytes.Buffer) *Deps {
	return &Deps{
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Stdout: stdout,
		Stderr: stderr,
		Now:    func() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
}

// testCLI keeps invocation setup concise while exercising the real Kong parser.
type testCLI struct {
	d       *Deps
	version string
	ctx     context.Context
	args    []string
	stdout  io.Writer
	stderr  io.Writer
}

func newTestCLI(d *Deps, version string) *testCLI {
	return &testCLI{d: d, version: version, ctx: context.Background()}
}

func (c *testCLI) SetArgs(args []string)          { c.args = args }
func (c *testCLI) SetOut(w io.Writer)             { c.stdout = w }
func (c *testCLI) SetErr(w io.Writer)             { c.stderr = w }
func (c *testCLI) SetContext(ctx context.Context) { c.ctx = ctx }
func (c *testCLI) Execute() error {
	stdout, stderr := c.stdout, c.stderr
	if stdout == nil {
		stdout = c.d.Stdout
	}
	if stderr == nil {
		stderr = c.d.Stderr
	}
	return Execute(c.ctx, c.args, stdout, stderr, c.d, c.version)
}
func (c *testCLI) ExecuteContext(ctx context.Context) error {
	stdout, stderr := c.stdout, c.stderr
	if stdout == nil {
		stdout = c.d.Stdout
	}
	if stderr == nil {
		stderr = c.d.Stderr
	}
	return Execute(ctx, c.args, stdout, stderr, c.d, c.version)
}

type commandErrorStore struct {
	app.Store
	err error
}

type commandFailWriter struct{}

func (commandFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (s *commandErrorStore) List(context.Context) ([]task.Task, error) { return nil, s.err }
func (s *commandErrorStore) Get(context.Context, string) (task.Task, error) {
	return task.Task{}, s.err
}
func (s *commandErrorStore) Counts(context.Context) (map[task.Status]int, error) {
	return nil, s.err
}
func (s *commandErrorStore) ActiveLists(context.Context) ([]task.Task, []task.Task, error) {
	return nil, nil, s.err
}
func (s *commandErrorStore) Ready(context.Context) ([]task.Task, error) { return nil, s.err }
func (s *commandErrorStore) Update(context.Context, string, task.EventType, string, func(*task.Task) bool) error {
	return s.err
}
func (s *commandErrorStore) UpdateGuarded(context.Context, string, task.EventType, string, func(*task.Task) bool) error {
	return s.err
}
func (s *commandErrorStore) ClaimNextForWorker(context.Context, time.Time, time.Time, string, string) (*task.Task, error) {
	return nil, s.err
}
func (s *commandErrorStore) Heartbeat(context.Context, string, string, time.Time, time.Time) error {
	return s.err
}
func (s *commandErrorStore) RequeueStale(context.Context, time.Duration, time.Time) ([]task.Task, error) {
	return nil, s.err
}

type commandInvalidClaimStore struct {
	commandErrorStore
	claimed task.Task
}

func (s *commandInvalidClaimStore) ClaimNextForWorker(context.Context, time.Time, time.Time, string, string) (*task.Task, error) {
	return &s.claimed, nil
}

type commandDependencyErrorStore struct {
	app.Store
	tasks []task.Task
}

func (s *commandDependencyErrorStore) List(context.Context) ([]task.Task, error) {
	return append([]task.Task(nil), s.tasks...), nil
}

func (s *commandDependencyErrorStore) Add(_ context.Context, t task.Task) error {
	s.tasks = append(s.tasks, t)
	return nil
}

func (s *commandDependencyErrorStore) AddDependency(context.Context, string, string) error {
	return errors.New("dependency boom")
}

func (s *commandDependencyErrorStore) Delete(_ context.Context, id string) error {
	for i, t := range s.tasks {
		if t.ID == id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			return nil
		}
	}
	return app.ErrNotFound
}
