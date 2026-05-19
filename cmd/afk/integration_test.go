//go:build integration

package main_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAfkLifecycle(t *testing.T) {
	// ── Build binary ──────────────────────────────────────────────────────────
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "afk")

	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, "./cmd/afk")
	buildCmd.Dir = filepath.Join("..", "..")
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "build failed: %s", out)

	// ── Queue path ────────────────────────────────────────────────────────────
	queueDir := t.TempDir()
	q := filepath.Join(queueDir, "q.jsonl")

	// ── Helper ────────────────────────────────────────────────────────────────
	run := func(args ...string) (stdout string, stderr string, exit int) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		fullArgs := append([]string{"--queue", q}, args...)
		cmd := exec.CommandContext(ctx, binPath, fullArgs...)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		exit = 0
		if runErr != nil {
			if ee, ok := runErr.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else {
				exit = 1
			}
		}
		return outBuf.String(), errBuf.String(), exit
	}

	idRe := regexp.MustCompile(`^\S+`)

	// ── add hello world ───────────────────────────────────────────────────────
	stdout, _, code := run("add", "hello", "world")
	require.Equal(t, 0, code, "add: unexpected exit code")
	require.NotEmpty(t, strings.TrimSpace(stdout), "add: expected id on stdout")

	id := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, id, "add: could not capture id from output %q", stdout)

	// ── count → pending: 1 ───────────────────────────────────────────────────
	stdout, _, code = run("count")
	require.Equal(t, 0, code, "count: unexpected exit code")
	require.Contains(t, stdout, "pending: 1", "count: expected pending: 1")

	// ── ls → contains id and body ─────────────────────────────────────────────
	stdout, _, code = run("ls")
	require.Equal(t, 0, code, "ls: unexpected exit code")
	require.Contains(t, stdout, id, "ls: expected id in output")
	require.Contains(t, stdout, "hello world", "ls: expected body in output")

	// ── ls --json → JSONL, length 1 ───────────────────────────────────────────
	stdout, _, code = run("ls", "--json")
	require.Equal(t, 0, code, "ls --json: unexpected exit code")
	var jsonlTasks []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &obj), "ls --json: invalid JSON line: %q", line)
		jsonlTasks = append(jsonlTasks, obj)
	}
	require.Len(t, jsonlTasks, 1, "ls --json: expected 1 task")
	require.NotEmpty(t, jsonlTasks[0]["cwd"], "ls --json: expected cwd metadata from add")

	// ── show <id> → contains body ─────────────────────────────────────────────
	stdout, _, code = run("show", id)
	require.Equal(t, 0, code, "show: unexpected exit code")
	require.Contains(t, stdout, "hello world", "show: expected body in output")

	// ── show <id> --json → valid JSON object with id field ────────────────────
	stdout, _, code = run("show", id, "--json")
	require.Equal(t, 0, code, "show --json: unexpected exit code")
	var showObj map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &showObj), "show --json: invalid JSON")
	require.Equal(t, id, showObj["id"], "show --json: id field mismatch")
	require.NotEmpty(t, showObj["cwd"], "show --json: expected cwd metadata")

	// ── next → contains id ────────────────────────────────────────────────────
	stdout, _, code = run("next")
	require.Equal(t, 0, code, "next: unexpected exit code")
	require.Contains(t, stdout, id, "next: expected id in output")

	// ── edit <id> hello goodbye ───────────────────────────────────────────────
	_, _, code = run("edit", id, "hello", "goodbye")
	require.Equal(t, 0, code, "edit: unexpected exit code")
	stdout, _, code = run("show", id)
	require.Equal(t, 0, code, "show after edit: unexpected exit code")
	require.Contains(t, stdout, "hello goodbye", "show after edit: expected new body")
	require.NotContains(t, stdout, "hello world", "show after edit: old body should be gone")

	// ── done <id> → count: done: 1, pending: 0 ───────────────────────────────
	_, _, code = run("done", id)
	require.Equal(t, 0, code, "done: unexpected exit code")
	stdout, _, code = run("count")
	require.Equal(t, 0, code, "count after done: unexpected exit code")
	require.Contains(t, stdout, "done: 1", "count after done: expected done: 1")
	require.Contains(t, stdout, "pending: 0", "count after done: expected pending: 0")

	// ── reset <id> → count: pending: 1, done: 0 ──────────────────────────────
	_, _, code = run("reset", id)
	require.Equal(t, 0, code, "reset: unexpected exit code")
	stdout, _, code = run("count")
	require.Equal(t, 0, code, "count after reset: unexpected exit code")
	require.Contains(t, stdout, "pending: 1", "count after reset: expected pending: 1")
	require.Contains(t, stdout, "done: 0", "count after reset: expected done: 0")

	// ── fail <id> oops → show contains "failed" and "oops" ───────────────────
	_, _, code = run("fail", id, "oops")
	require.Equal(t, 0, code, "fail: unexpected exit code")
	stdout, _, code = run("show", id)
	require.Equal(t, 0, code, "show after fail: unexpected exit code")
	require.Contains(t, stdout, "failed", "show after fail: expected status failed")
	require.Contains(t, stdout, "oops", "show after fail: expected error reason")

	// ── prune → all zeros ─────────────────────────────────────────────────────
	_, _, code = run("prune")
	require.Equal(t, 0, code, "prune: unexpected exit code")
	stdout, _, code = run("count")
	require.Equal(t, 0, code, "count after prune: unexpected exit code")
	require.Contains(t, stdout, "pending: 0", "count after prune: expected pending: 0")
	require.Contains(t, stdout, "done: 0", "count after prune: expected done: 0")
	require.Contains(t, stdout, "failed: 0", "count after prune: expected failed: 0")

	// ── add task-a, task-b, task-c ────────────────────────────────────────────
	stdout, _, code = run("add", "task-a")
	require.Equal(t, 0, code, "add task-a: unexpected exit code")
	idA := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, idA)

	stdout, _, code = run("add", "task-b")
	require.Equal(t, 0, code, "add task-b: unexpected exit code")
	idB := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, idB)

	stdout, _, code = run("add", "task-c")
	require.Equal(t, 0, code, "add task-c: unexpected exit code")
	idC := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, idC)

	// ── pop → valid JSON with one of those ids, status "working" ─────────────
	stdout, _, code = run("pop", "--lease", "30m")
	require.Equal(t, 0, code, "pop: unexpected exit code")
	var popObj map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &popObj), "pop: invalid JSON")
	poppedID, _ := popObj["id"].(string)
	require.Contains(t, []string{idA, idB, idC}, poppedID, "pop: id should be one of the three added tasks")
	require.Equal(t, "working", popObj["status"], "pop: expected status working")
	require.NotEmpty(t, popObj["lease_expires"], "pop: expected lease_expires")

	// ── explain <id> → includes ledger views ─────────────────────────────────
	stdout, _, code = run("explain", poppedID)
	require.Equal(t, 0, code, "explain: unexpected exit code")
	require.Contains(t, stdout, "Events:")
	require.Contains(t, stdout, "Attempts:")

	// ── count → working: 1, pending: 2 ───────────────────────────────────────
	stdout, _, code = run("count")
	require.Equal(t, 0, code, "count after pop: unexpected exit code")
	require.Contains(t, stdout, "working: 1", "count after pop: expected working: 1")
	require.Contains(t, stdout, "pending: 2", "count after pop: expected pending: 2")

	// ── rm a pending id → count: working: 1, pending: 1 ─────────────────────
	// poppedID is working; pick one of the remaining two pending ids to remove.
	var pendingToRemove string
	for _, candidate := range []string{idA, idB, idC} {
		if candidate != poppedID {
			pendingToRemove = candidate
			break
		}
	}
	_, _, code = run("rm", pendingToRemove)
	require.Equal(t, 0, code, "rm: unexpected exit code")
	stdout, _, code = run("count")
	require.Equal(t, 0, code, "count after rm: unexpected exit code")
	require.Contains(t, stdout, "working: 1", "count after rm: expected working: 1")
	require.Contains(t, stdout, "pending: 1", "count after rm: expected pending: 1")

	// ── Error cases ───────────────────────────────────────────────────────────

	// show nonexistent-id → exit ≠ 0, stderr contains "not found"
	_, stderr, code := run("show", "nonexistent-id")
	require.NotEqual(t, 0, code, "show nonexistent: expected non-zero exit")
	require.Contains(t, stderr, "not found", "show nonexistent: expected 'not found' in stderr")

	// done nonexistent-id → exit ≠ 0
	_, _, code = run("done", "nonexistent-id")
	require.NotEqual(t, 0, code, "done nonexistent: expected non-zero exit")

	// retry failed task → pending again
	stdout, _, code = run("add", "retry-me")
	require.Equal(t, 0, code, "add retry-me: unexpected exit code")
	retryID := idRe.FindString(strings.TrimSpace(stdout))
	_, _, code = run("fail", retryID, "retryable")
	require.Equal(t, 0, code, "fail retry-me: unexpected exit code")
	_, _, code = run("retry", retryID)
	require.Equal(t, 0, code, "retry: unexpected exit code")
	stdout, _, code = run("show", retryID)
	require.Equal(t, 0, code, "show after retry: unexpected exit code")
	require.Contains(t, stdout, "Status: pending")

	// ── SQLite-backed queue integrity: DB exists and public JSONL output parses ─
	sqlitePath := strings.TrimSuffix(q, filepath.Ext(q)) + ".sqlite"
	_, err = os.Stat(sqlitePath)
	require.NoError(t, err, "sqlite queue should exist")

	stdout, _, code = run("ls", "--json")
	require.Equal(t, 0, code, "final ls --json: unexpected exit code")
	lineScanner := bufio.NewScanner(strings.NewReader(stdout))
	lineNum := 0
	for lineScanner.Scan() {
		lineNum++
		line := strings.TrimSpace(lineScanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &obj),
			"ls --json line %d is not valid JSON: %q", lineNum, line)
	}
	require.NoError(t, lineScanner.Err(), "ls --json scan error")

	// ── prompt → current loop Markdown on stdout ──────────────────────────────
	stdout, _, code = run("prompt")
	require.Equal(t, 0, code, "prompt: unexpected exit code")
	require.Contains(t, stdout, "Loop Tick")
	require.Contains(t, stdout, "afk pop")
	require.Contains(t, stdout, "afk done <id>")
	require.Contains(t, stdout, "afk fail <id>")
	require.Contains(t, stdout, sqlitePath)
	require.Contains(t, stdout, "`cwd`")

	stdout, _, code = run("prompt", "--task", retryID)
	require.Equal(t, 0, code, "prompt --task: unexpected exit code")
	require.Contains(t, stdout, "AFK Task "+retryID)
	require.Contains(t, stdout, "retry-me")

	stdout, _, code = run("doctor")
	require.Equal(t, 0, code, "doctor: unexpected exit code")
	require.Contains(t, stdout, "db: ok")
	require.Contains(t, stdout, "prompt: ok")

	// ── prompt --output → writes Markdown to file ────────────────────────────
	promptPath := filepath.Join(queueDir, "loop.md")
	stdout, _, code = run("prompt", "--output", promptPath)
	require.Equal(t, 0, code, "prompt --output: unexpected exit code")
	require.Empty(t, stdout, "prompt --output should not write stdout")
	promptBytes, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	require.Contains(t, string(promptBytes), "Do not pick up another task this tick")
}
