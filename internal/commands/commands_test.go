package commands

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommandsLifecycleThroughRoot(t *testing.T) {
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

	id := strings.TrimSpace(run("add", "hello", "world"))
	require.NotEmpty(t, id)
	require.Contains(t, run("ls"), "hello world")
	require.Contains(t, run("ls", "--status", "pending", "--json"), id)

	showJSON := run("show", id, "--json")
	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(showJSON)), &shown))
	require.Equal(t, id, shown["id"])

	require.Contains(t, run("next"), id)
	run("edit", id, "updated")
	require.Contains(t, run("show", id), "updated")

	run("done", id)
	require.Contains(t, run("count"), "done: 1")
	run("reset", id)
	run("fail", id, "oops")
	require.Contains(t, run("show", id), "Error: oops")
	run("prune")
	require.Contains(t, run("count"), "failed: 0")

	id = strings.TrimSpace(run("add", "pop-me"))
	popOut := run("pop", "--lease", "30m", "--worker", "worker-1")
	require.Contains(t, popOut, id)
	require.Contains(t, popOut, `"status":"working"`)
	require.Contains(t, popOut, `"lease_expires"`)
	explain := run("explain", id)
	require.Contains(t, explain, "Events:")
	require.Contains(t, explain, "worker=worker-1")
	require.Contains(t, run("prompt", "--task", id), "AFK Task "+id)
	run("rm", id)
	require.Contains(t, run("count"), "working: 0")
}

func TestCommandsRetryRequeueStaleAndDoctor(t *testing.T) {
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

	id := strings.TrimSpace(run("add", "--no-cwd", "retry me"))
	run("fail", id, "boom")
	run("retry", id)
	require.Contains(t, run("show", id), "Status: pending")
	run("rm", id)

	id = strings.TrimSpace(run("add", "--no-cwd", "stale"))
	require.Contains(t, run("pop", "--lease", "1ns"), id)
	time.Sleep(time.Millisecond)
	require.Contains(t, run("requeue-stale", "--older-than", "1h"), id)
	require.Contains(t, run("show", id), "Status: pending")

	doctor := run("doctor")
	require.Contains(t, doctor, "db: ok")
	require.Contains(t, doctor, "prompt: ok")
}

func TestAddCommandRecordsMetadataAndDefaultCWD(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"add",
		"--tag", "repo:afk",
		"--tag", "type:test",
		"--priority", "high",
		"--source", "cli",
		"--agent", "codex",
		"--group", "group",
		"--resource", "repo:" + dir,
		"metadata task",
	})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	id := strings.TrimSpace(stdout.String())
	require.NotEmpty(t, id)

	stdout.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "show", id, "--json"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &shown))
	require.Equal(t, "high", shown["priority"])
	require.Equal(t, "cli", shown["source"])
	require.Equal(t, "codex", shown["agent"])
	require.Equal(t, "group", shown["group_id"])
	require.NotEmpty(t, shown["cwd"])
	require.Equal(t, []any{"repo:afk", "type:test"}, shown["tags"])
}

func TestAddCommandCanDisableCWD(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "--no-cwd", "no cwd"})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	id := strings.TrimSpace(stdout.String())
	stdout.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "show", id, "--json"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &shown))
	require.NotContains(t, shown, "cwd")
}

func TestDependencyCommands(t *testing.T) {
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

	prereq := strings.TrimSpace(run("add", "--no-cwd", "prereq"))
	blocked := strings.TrimSpace(run("add", "--no-cwd", "--blocked-by", prereq, "blocked"))
	require.Contains(t, run("deps", "ls", blocked), prereq)

	depsJSON := run("deps", "ls", blocked, "--json")
	var deps []map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(depsJSON)), &deps))
	require.Len(t, deps, 1)
	require.Equal(t, blocked, deps[0]["task_id"])
	require.Equal(t, prereq, deps[0]["depends_on_id"])

	run("deps", "rm", blocked, "--blocked-by", prereq)
	require.Empty(t, run("deps", "ls", blocked))

	aliasBlocked := strings.TrimSpace(run("add", "--no-cwd", "--after", prereq, "alias blocked"))
	require.Contains(t, run("deps", "ls", aliasBlocked), prereq)
	none := strings.TrimSpace(run("add", "--no-cwd", "--blocked-by", "none", "none"))
	require.Empty(t, run("deps", "ls", none))
}

func TestReadyAndWhyCommands(t *testing.T) {
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

	prereq := strings.TrimSpace(run("add", "--no-cwd", "prereq"))
	blocked := strings.TrimSpace(run("add", "--no-cwd", "--blocked-by", prereq, "blocked"))
	independent := strings.TrimSpace(run("add", "--no-cwd", "independent"))

	ready := run("ready")
	require.Contains(t, ready, prereq)
	require.Contains(t, ready, independent)
	require.NotContains(t, ready, blocked)

	why := run("why", blocked)
	require.Contains(t, why, "Ready: false")
	require.Contains(t, why, "waiting on task "+prereq)

	run("fail", prereq, "boom")
	why = run("why", blocked)
	require.Contains(t, why, "blocked by failed task "+prereq)

	whyJSON := run("why", blocked, "--json")
	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(whyJSON)), &data))
	require.Equal(t, false, data["ready"])
}

func TestBlockCommands(t *testing.T) {
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

	id := strings.TrimSpace(run("add", "--no-cwd", "blocked"))
	run("block", id, "waiting", "on", "credentials")
	require.NotContains(t, run("ready"), id)
	require.Contains(t, run("why", id), "blocked: waiting on credentials")

	run("unblock", id)
	require.Contains(t, run("ready"), id)
	require.Contains(t, run("show", id), "Status: pending")
}

func TestHeartbeatCommand(t *testing.T) {
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

	id := strings.TrimSpace(run("add", "--no-cwd", "heartbeat"))
	run("pop", "--worker", "worker-1", "--lease", "1m")
	run("heartbeat", id, "--worker", "worker-1", "--lease", "30m")
	show := run("show", id)
	require.Contains(t, show, "Started: 2025-01-02T03:04:05Z")
	require.Contains(t, run("explain", id), "heartbeat  worker-1")
}

func TestRunCommandDryRunShowsRunnableAndWaitingTasks(t *testing.T) {
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

	prereq := strings.TrimSpace(run("add", "--no-cwd", "prereq"))
	blocked := strings.TrimSpace(run("add", "--no-cwd", "--blocked-by", prereq, "blocked"))
	ready := strings.TrimSpace(run("add", "--no-cwd", "ready"))

	out := run("run", "--dry-run", "--limit", "0", "--exec", "echo {{id}}")
	require.Contains(t, out, "WOULD_RUN")
	require.Contains(t, out, prereq)
	require.Contains(t, out, ready)
	require.Contains(t, out, "WAITING")
	require.Contains(t, out, blocked)
	require.Contains(t, out, "dependency_pending: "+prereq)
}

func TestRunCommandExecutesOnlyReadyTaskAndFailsIfUnfinalized(t *testing.T) {
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

	ready := strings.TrimSpace(run("add", "--no-cwd", "ready"))
	blocked := strings.TrimSpace(run("add", "--no-cwd", "--blocked-by", ready, "blocked"))

	out := run("run", "--exec", "test -n \"$AFK_QUEUE\" && test -f {{queue}}", "--worker", "runner-1", "--lease", "1m")
	require.Contains(t, out, "running "+ready)

	showReady := run("show", ready)
	require.Contains(t, showReady, "Status: failed")
	require.Contains(t, showReady, "runner command exited without finalizing task")
	require.Contains(t, run("show", blocked), "Status: pending")
}

func TestRunCommandRejectsUnsupportedWorkers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(testDepsWithWriters(stdout, stderr), "test")
	root.SetArgs([]string{"--queue", filepath.Join(dir, "tasks.sqlite"), "run", "--workers", "2", "--exec", "true"})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--workers > 1")
}

func TestWhyReportsResourceLock(t *testing.T) {
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

	first := strings.TrimSpace(run("add", "--no-cwd", "--resource", "repo:x", "first"))
	second := strings.TrimSpace(run("add", "--no-cwd", "--resource", "repo:x", "second"))
	require.Contains(t, run("pop"), first)
	require.Contains(t, run("why", second), "resource active on task "+first)
}

func TestAddBlockedByMissingTaskReturnsErrorWithoutAddingTask(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "--no-cwd", "--blocked-by", "missing", "blocked"})
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	stdout.Reset()
	stderr.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "count"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	require.Contains(t, stdout.String(), "pending: 0")
}

func TestCommandsMissingTaskReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(testDepsWithWriters(stdout, stderr), "test")
	root.SetArgs([]string{"--queue", filepath.Join(dir, "tasks.sqlite"), "show", "missing"})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func testDepsWithWriters(stdout, stderr *bytes.Buffer) *Deps {
	return &Deps{
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Stdout: stdout,
		Stderr: stderr,
		Now:    func() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
}
