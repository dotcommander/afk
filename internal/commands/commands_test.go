package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/task"
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

func TestDiscoverCommandPrintsStubWithoutCreatingQueue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(testDepsWithWriters(stdout, stderr), "test")
	root.SetArgs([]string{"--queue", queuePath, "discover"})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	require.Contains(t, stdout.String(), "afk discover is a workflow stub")
	require.Contains(t, stdout.String(), "docs/task-discovery.md")
	require.NoFileExists(t, queuePath)
}

func TestDoctorReportIncludesTaskCountsAndWorkingWarning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	require.NoError(t, os.WriteFile(queuePath, nil, 0o600))

	var out bytes.Buffer
	snapshot := summarizeDoctorTasks([]task.Task{
		{Status: task.StatusPending},
		{Status: task.StatusWorking},
		{Status: task.StatusDone},
		{Status: task.StatusFailed},
	})
	require.NoError(t, writeDoctorReport(&out, queuePath, snapshot))

	report := out.String()
	require.Contains(t, report, "queue: "+queuePath)
	require.Contains(t, report, "db: ok")
	require.Contains(t, report, "pending: 1")
	require.Contains(t, report, "working: 1")
	require.Contains(t, report, "done: 1")
	require.Contains(t, report, "failed: 1")
	require.Contains(t, report, "working tasks: 1")
	require.Contains(t, report, "prompt: ok")
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

func TestAddCommandInfersRepoDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repo := filepath.Join(dir, "project")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "--cwd", repo, "repo defaults"})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	id := strings.TrimSpace(stdout.String())
	require.NotEmpty(t, id)
	require.Empty(t, stderr.String(), "non-tty stderr must stay script-friendly")

	stdout.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "show", id, "--json"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &shown))
	require.Equal(t, "cli", shown["source"])
	require.Equal(t, repo, shown["cwd"])
	require.Equal(t, "repo:"+repo, shown["resource_key"])
	require.Equal(t, []any{"repo:project"}, shown["tags"])
}

func TestAddCommandExplicitMetadataOverridesRepoDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repo := filepath.Join(dir, "project")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"add",
		"--cwd", repo,
		"--source", "roadmap.md",
		"--tag", "type:docs",
		"--resource", "none",
		"explicit defaults",
	})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	id := strings.TrimSpace(stdout.String())

	stdout.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "show", id, "--json"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	var shown map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &shown))
	require.Equal(t, "roadmap.md", shown["source"])
	require.NotContains(t, shown, "resource_key")
	require.Equal(t, []any{"type:docs"}, shown["tags"])
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
	require.Equal(t, "cli", shown["source"])
	require.NotContains(t, shown, "resource_key")
}

func TestAddCommandRejectsInvalidTask(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "pick", "my", "nose"})

	err := root.Execute()
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
	require.Empty(t, stdout.String())
}

func TestAddCommandRejectsInvalidPriority(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "--no-cwd", "--priority", "hihg", "priority typo"})

	err := root.Execute()
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidPriority), "got %v", err)
	require.Contains(t, err.Error(), "urgent, high, normal, or low")
	require.Empty(t, stdout.String())
}

func TestAddDryRunValidatesWithoutAddingTask(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"add",
		"--dry-run",
		"--json",
		"--source", "task-discovery",
		"--cwd", dir,
		"[discovery:afk:validate] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix the focused issue. Verify with go test ./...",
	})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	var dryRun map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &dryRun))
	require.Equal(t, true, dryRun["valid"])

	stdout.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "count"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	require.Contains(t, stdout.String(), "pending: 0")
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

func TestPrioritySchedulingCommands(t *testing.T) {
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

	normal := strings.TrimSpace(run("add", "--no-cwd", "normal"))
	urgent := strings.TrimSpace(run("add", "--no-cwd", "--priority", "urgent", "urgent"))
	high := strings.TrimSpace(run("add", "--no-cwd", "--priority", "high", "high"))

	readyLines := strings.Split(strings.TrimSpace(run("ready", "--json")), "\n")
	require.Len(t, readyLines, 3)
	var firstReady map[string]any
	require.NoError(t, json.Unmarshal([]byte(readyLines[0]), &firstReady))
	require.Equal(t, urgent, firstReady["id"])

	var next map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("next", "--json"))), &next))
	require.Equal(t, urgent, next["id"])

	dryRun := run("run", "--dry-run", "--limit", "0", "--exec", "echo {{id}}")
	dryRunLines := strings.Split(strings.TrimSpace(dryRun), "\n")
	require.Len(t, dryRunLines, 4)
	require.True(t, strings.HasPrefix(dryRunLines[1], urgent+"\t") || strings.HasPrefix(dryRunLines[1], urgent+" "))
	require.True(t, strings.HasPrefix(dryRunLines[2], high+"\t") || strings.HasPrefix(dryRunLines[2], high+" "))
	require.True(t, strings.HasPrefix(dryRunLines[3], normal+"\t") || strings.HasPrefix(dryRunLines[3], normal+" "))

	var popped map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("pop"))), &popped))
	require.Equal(t, urgent, popped["id"])
}

func TestTopCommandPromotesPendingTask(t *testing.T) {
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

	first := strings.TrimSpace(run("add", "--no-cwd", "first"))
	second := strings.TrimSpace(run("add", "--no-cwd", "second"))
	urgent := strings.TrimSpace(run("add", "--no-cwd", "--priority", "urgent", "urgent"))

	require.Equal(t, second+"\n", run("top", second))
	var next map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("next", "--json"))), &next))
	require.Equal(t, urgent, next["id"], "promotion must not outrank urgent priority")

	run("done", urgent)
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(run("next", "--json"))), &next))
	require.Equal(t, second, next["id"])
	require.Contains(t, run("explain", second), "promoted")

	run("done", second)
	stdout.Reset()
	stderr.Reset()
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "top", second})
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid state")

	require.NotEmpty(t, first)
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

func TestNextJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	// Empty queue → "{}\n"
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "next", "--json"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	require.Equal(t, "{}\n", stdout.String())

	// Add a task
	stdout.Reset()
	stderr.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "--no-cwd", "smoke task"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	addedID := strings.TrimSpace(stdout.String())
	require.NotEmpty(t, addedID)

	// next --json should return that task
	stdout.Reset()
	stderr.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "next", "--json"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &doc))
	require.Equal(t, addedID, doc["id"])
	require.Equal(t, "smoke task", doc["body"])
}

func TestNextAndPopJSONBoundLargeBodies(t *testing.T) {
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

	run("add", "--no-cwd", strings.Repeat("x", 9000))
	for _, out := range []string{run("next", "--json"), run("pop")} {
		var doc map[string]any
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &doc))
		body, ok := doc["body"].(string)
		require.True(t, ok)
		require.Equal(t, true, doc["body_truncated"])
		require.NotContains(t, body, strings.Repeat("x", 8500))
	}
}

func TestAddCommandJSONFlag(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "--no-cwd", "--json", "json body"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &doc))
	idVal, ok := doc["id"].(string)
	require.True(t, ok, "id field must be a string")
	require.NotEmpty(t, idVal)
	require.Len(t, doc, 1, "only id key expected")

	stdout.Reset()
	stderr.Reset()
	root = NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "add", "--no-cwd", "plain body"})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	plain := strings.TrimSpace(stdout.String())
	require.NotEmpty(t, plain)
	require.NotContains(t, plain, "{")
	require.NotContains(t, plain, "}")
	require.NotEqual(t, idVal, plain, "second add should yield distinct id")
}

func TestAddDiagnoseReportsAllFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"add", "--diagnose",
		"--source", "task-discovery",
		"--cwd", dir,
		"Scope: /tmp/repo/file.go. Fix /tmp/repo/file.go.",
	})

	err := root.Execute()
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask))

	out := stderr.String()
	require.Contains(t, out, "must start with [discovery:")
	require.Contains(t, out, "verification command")
	require.Contains(t, out, "must include evidence")
	require.NoFileExists(t, filepath.Join(dir, "rejected.jsonl"), "diagnose must not write rejection sidecar")
}

func TestAddDiscoveryFailureSuggestsDiagnose(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"add",
		"--source", "task-discovery",
		"--cwd", dir,
		"Fix the focused issue.",
	})

	err := root.Execute()
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask))
	require.Contains(t, err.Error(), "--diagnose")
	require.Contains(t, err.Error(), "remove --source task-discovery/--tag discovery")
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())
}

func TestAddDiagnoseAcceptsValidGeneratedBody(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"add", "--diagnose",
		"--source", "task-discovery",
		"--cwd", dir,
		"[discovery:afk:diag] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix the focused issue. Verify with go test ./...",
	})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	require.Contains(t, stdout.String(), "task validates")
	require.NoFileExists(t, filepath.Join(dir, "rejected.jsonl"), "diagnose must not write rejection sidecar on success")
}

func TestRejectedLsEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(testDepsWithWriters(stdout, stderr), "test")
	root.SetArgs([]string{"--queue", queuePath, "rejected", "ls"})

	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	require.Contains(t, stdout.String(), "no rejected tasks")
}

func TestAddDiagnoseDoesNotInsertRow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	runCmd := func(args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
		return stdout.String()
	}

	id := strings.TrimSpace(runCmd("add", "--no-cwd", "seed task"))
	require.NotEmpty(t, id)
	before := runCmd("count")

	stdout.Reset()
	stderr.Reset()
	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"add", "--diagnose",
		"--source", "task-discovery",
		"--cwd", dir,
		"[discovery:afk:diag] Evidence: /tmp/repo/file.go:1. Scope: /tmp/repo/file.go. Fix the focused issue. Verify with go test ./...",
	})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())

	after := runCmd("count")
	require.Equal(t, before, after, "diagnose must not change task counts")
	require.NoFileExists(t, filepath.Join(dir, "rejected.jsonl"), "diagnose must not write rejection sidecar")
}
