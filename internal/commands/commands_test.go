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
	popOut := run("pop", "--lease", "30m")
	require.Contains(t, popOut, id)
	require.Contains(t, popOut, `"status":"working"`)
	require.Contains(t, popOut, `"lease_expires"`)
	require.Contains(t, run("explain", id), "Events:")
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
