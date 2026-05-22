package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dotcommander/afk/internal/app"
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
		root := NewRoot(d, "test")
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

	require.Equal(t, "set "+id+" done\n", run("set", id, "done"))
	require.Contains(t, run("status"), "done: 1")
	require.NotContains(t, run("tasks", "--status", "todo"), id)

	failedID := strings.TrimSpace(run("add", "--no-cwd", "fail-me"))
	require.JSONEq(t, `{"id":"`+failedID+`","status":"failed","title":"fail-me","note":"oops"}`, run("set", failedID, "failed", "oops", "--json"))
	require.Contains(t, run("task", failedID), "Error: oops")

	require.Equal(t, "set "+failedID+" deleted\n", run("set", failedID, "deleted", "cleanup"))
	require.NotContains(t, run("tasks"), failedID)
	require.Contains(t, run("tasks", "--status", "deleted"), failedID)
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
		root := NewRoot(d, "test")
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
	root := NewRoot(d, "test")
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
		root := NewRoot(d, "test")
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
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		return root.Execute()
	}

	require.NoError(t, run("add", "--no-cwd", "--resource", "repo:one", "first ready task"))
	require.NoError(t, run("take", "--worker", "worker-1", "--lease", "30m"))
	require.NoError(t, run("add", "--no-cwd", "--resource", "repo:one", "resource locked task"))

	require.NoError(t, run("take"))
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "No ready tasks")
	require.Contains(t, stderr.String(), "1 todo task(s) blocked by active resource locks")
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
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	firstID := strings.TrimSpace(run("add", "--no-cwd", "first task"))
	secondID := strings.TrimSpace(run("add", "--no-cwd", "second task"))
	doneID := strings.TrimSpace(run("add", "--no-cwd", "done task"))
	run("set", doneID, "done")

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

func TestSetNoteFlagsAndSummary(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := NewRoot(d, "test")
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
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "retry task"))
	run("take")
	run("set", id, "failed", "blocked")
	run("set", id, "doing", "retrying")
	run("set", id, "done")

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

func TestRetryCommandCreatesRetryAttempt(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "retry command task"))
	run("take")
	run("set", id, "failed", "workspace permission blocked")

	require.JSONEq(t, `{"id":"`+id+`","status":"doing","note":"retrying: workspace permission approved"}`, run("retry", id, "--reason", "workspace permission approved", "--json"))
	run("set", id, "done")

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
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(run("add", "--no-cwd", "retry default task"))
	require.Equal(t, "retry "+id+" doing\n", run("retry", id))
	require.Contains(t, run("task", id), "retrying")
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
		root := NewRoot(d, "test")
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
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	todoID := strings.TrimSpace(run("add", "--no-cwd", "todo task"))
	doneID := strings.TrimSpace(run("add", "--no-cwd", "done task"))
	run("set", doneID, "done")
	require.Contains(t, run("take"), todoID)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("status", "--summary", "--json"))), &doc))
	require.Equal(t, float64(0), doc["todo"])
	require.Equal(t, float64(1), doc["doing"])
	require.Equal(t, float64(1), doc["done"])
	require.Equal(t, float64(0), doc["failed"])
	require.Equal(t, float64(0), doc["deleted"])
}

func TestOldCommandsAreNotPublic(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ls", "pop", "done", "fail", "explain", "run", "prune", "doctor"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := NewRoot(testDepsWithWriters(stdout, stderr), "test")
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
	root := NewRoot(testDepsWithWriters(stdout, stderr), "test")
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
		root := NewRoot(d, "test")
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
		root := NewRoot(d, "test")
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
		root := NewRoot(d, "test")
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
	require.True(t, isGeneratedAdd(task.AddOptions{Source: "task-discovery"}))
	require.True(t, isGeneratedAdd(task.AddOptions{Tags: []string{" discovery "}}))
	require.False(t, isGeneratedAdd(task.AddOptions{}))

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	require.NoError(t, writeAddDryRunResult(d, false))
	require.Equal(t, "valid\n", stdout.String())

	stdout.Reset()
	require.NoError(t, writeAddDryRunResult(d, true))
	require.JSONEq(t, `{"valid":true}`, strings.TrimSpace(stdout.String()))

	cmd := &cobra.Command{}
	err := addValidationError(cmd, task.AddOptions{Source: "task-discovery"}, task.ErrInvalidTask)
	require.Error(t, err)
	require.Contains(t, err.Error(), "generated/discovery tasks")
	require.True(t, cmd.SilenceUsage)
	require.True(t, cmd.SilenceErrors)
}

func TestJSONByIDCommandPassesIDAndJSONFlag(t *testing.T) {
	t.Parallel()

	var gotID string
	var gotJSON bool
	cmd := newJSONByIDCmd("thing <id>", "show thing", "emit json", func(_ context.Context, id string, asJSON bool) error {
		gotID = id
		gotJSON = asJSON
		return nil
	})
	cmd.SetArgs([]string{"abc", "--json"})
	require.NoError(t, cmd.Execute())
	require.Equal(t, "abc", gotID)
	require.True(t, gotJSON)
}

func TestCommandRunEPropagatesServiceErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	d.Service = app.NewService(&commandErrorStore{err: boom}, func() time.Time { return time.Now() })

	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{
		{name: "tasks", cmd: newTasksCmd(d)},
		{name: "find", cmd: newFindCmd(d), args: []string{"x"}},
		{name: "status", cmd: newStatusCmd(d)},
		{name: "task", cmd: newTaskCmd(d), args: []string{"id"}},
		{name: "set", cmd: newSetCmd(d), args: []string{"id", "done"}},
		{name: "take dry-run", cmd: newTakeCmd(d), args: []string{"--dry-run"}},
		{name: "take claim", cmd: newTakeCmd(d)},
		{name: "retry", cmd: newRetryCmd(d), args: []string{"id"}},
		{name: "snapshot", cmd: newSnapshotCmd(d)},
		{name: "requeue", cmd: newRequeueStaleCmd(d), args: []string{"--older-than", "1s"}},
		{name: "heartbeat", cmd: newHeartbeatCmd(d), args: []string{"id", "--worker", "worker"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.cmd.SetArgs(tc.args)
			err := tc.cmd.Execute()
			require.ErrorIs(t, err, boom)
		})
	}
}

func TestRunAddForcePropagatesDependencyError(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	d.Service = app.NewService(&commandDependencyErrorStore{}, func() time.Time { return time.Now() })
	t.Setenv("AFK_ALLOW_FORCE", "1")

	cmd := &cobra.Command{}
	err := runAddForce(cmd, d, task.AddOptions{Body: "forced"}, "dep", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency boom")
	require.Contains(t, stderr.String(), "warning: --force")
}

func TestRunAddForcePropagatesWarningWriteError(t *testing.T) {
	stdout := &bytes.Buffer{}
	d := testDepsWithWriters(stdout, &bytes.Buffer{})
	d.Stderr = commandFailWriter{}
	d.Service = app.NewService(&commandDependencyErrorStore{}, func() time.Time { return time.Now() })
	t.Setenv("AFK_ALLOW_FORCE", "1")

	err := runAddForce(&cobra.Command{}, d, task.AddOptions{Body: "forced"}, "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write failed")
}

func TestRunAddNormalPropagatesDependencyError(t *testing.T) {
	t.Parallel()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	d.Service = app.NewService(&commandDependencyErrorStore{}, func() time.Time { return time.Now() })

	cmd := &cobra.Command{}
	err := runAddNormal(cmd, d, task.AddOptions{Body: "normal"}, "dep", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency boom")
}

func TestRunAddNormalPropagatesResultWriteError(t *testing.T) {
	t.Parallel()

	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	d.Stdout = commandFailWriter{}
	d.Service = app.NewService(&commandDependencyErrorStore{}, func() time.Time { return time.Now() })

	err := runAddNormal(&cobra.Command{}, d, task.AddOptions{Body: "normal"}, "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write failed")
}

func TestRunAddDiagnosePropagatesWriterError(t *testing.T) {
	t.Parallel()

	d := testDepsWithWriters(&bytes.Buffer{}, &bytes.Buffer{})
	d.Stderr = commandFailWriter{}
	err := runAddDiagnose(&cobra.Command{}, d, task.AddOptions{
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
		root := NewRoot(d, "test")
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

	require.NoError(t, run("take", "--worker", "worker-1", "--lease", "1m"))
	require.NoError(t, run("heartbeat", id, "--worker", "worker-1", "--lease", "2m"))
	require.Contains(t, stderr.String(), "deprecated")

	err := run("requeue-stale", "--older-than", "soon")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse older-than")
}

func TestTakeRejectsInvalidClaimAndServeValidation(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	run := func(args ...string) error {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := NewRoot(d, "test")
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
	root := NewRoot(&Deps{
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
	serve := newServeCmd(badStderrDeps)
	serve.SetArgs([]string{"--addr", "0.0.0.0:0"})
	err = serve.Execute()
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

func (s *commandErrorStore) List(context.Context) ([]task.Task, error)  { return nil, s.err }
func (s *commandErrorStore) Ready(context.Context) ([]task.Task, error) { return nil, s.err }
func (s *commandErrorStore) Update(context.Context, string, task.EventType, string, func(*task.Task) bool) error {
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
