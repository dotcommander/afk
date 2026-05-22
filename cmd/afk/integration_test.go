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
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "afk")

	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, "./cmd/afk")
	buildCmd.Dir = filepath.Join("..", "..")
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "build failed: %s", out)

	queueDir := t.TempDir()
	q := filepath.Join(queueDir, "q.jsonl")
	sqlitePath := strings.TrimSuffix(q, filepath.Ext(q)) + ".sqlite"

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

	stdout, _, code := run("add", "--cwd", queueDir, "hello", "world")
	require.Equal(t, 0, code, "add: unexpected exit code")
	id := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, id, "add: could not capture id from output %q", stdout)

	stdout, _, code = run("status")
	require.Equal(t, 0, code, "status: unexpected exit code")
	require.Contains(t, stdout, "todo: 1", "status: expected todo: 1")

	stdout, _, code = run("tasks")
	require.Equal(t, 0, code, "tasks: unexpected exit code")
	require.Contains(t, stdout, id, "tasks: expected id in output")
	require.Contains(t, stdout, "hello world", "tasks: expected body in output")

	stdout, _, code = run("tasks", "--json")
	require.Equal(t, 0, code, "tasks --json: unexpected exit code")
	jsonlTasks := readJSONLines(t, stdout, "tasks --json")
	require.Len(t, jsonlTasks, 1, "tasks --json: expected 1 task")
	require.Equal(t, queueDir, jsonlTasks[0]["cwd"], "tasks --json: expected explicit cwd metadata")

	stdout, _, code = run("task", id)
	require.Equal(t, 0, code, "task: unexpected exit code")
	require.Contains(t, stdout, "hello world", "task: expected body in output")

	stdout, _, code = run("task", id, "--json")
	require.Equal(t, 0, code, "task --json: unexpected exit code")
	var taskDoc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &taskDoc), "task --json: invalid JSON")
	taskObj, ok := taskDoc["task"].(map[string]any)
	require.True(t, ok, "task --json: expected task object")
	require.Equal(t, id, taskObj["id"], "task --json: id field mismatch")
	require.Equal(t, queueDir, taskObj["cwd"], "task --json: expected cwd metadata")

	stdout, _, code = run("take", "--dry-run", "--limit", "1", "--json")
	require.Equal(t, 0, code, "take --dry-run --limit 1: unexpected exit code")
	require.Contains(t, stdout, id, "take --dry-run --limit 1: expected id in output")

	stdout, _, code = run("set", id, "done")
	require.Equal(t, 0, code, "set done: unexpected exit code")
	require.Equal(t, "set "+id+" done\n", stdout, "set done: expected confirmation")
	stdout, _, code = run("status")
	require.Equal(t, 0, code, "status after done: unexpected exit code")
	require.Contains(t, stdout, "todo: 0", "status after done: expected todo: 0")
	require.Contains(t, stdout, "done: 1", "status after done: expected done: 1")
	stdout, _, code = run("snapshot", "--label", "after-done", "--task", id)
	require.Equal(t, 0, code, "snapshot: unexpected exit code")
	var snapshotDoc map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &snapshotDoc), "snapshot: invalid JSON")
	require.Equal(t, "after-done", snapshotDoc["label"], "snapshot: label mismatch")
	require.Contains(t, stdout, `"counts"`, "snapshot: expected counts")
	require.Contains(t, stdout, `"task"`, "snapshot: expected task detail")

	stdout, _, code = run("add", "--no-cwd", "fail-me")
	require.Equal(t, 0, code, "add fail-me: unexpected exit code")
	id = idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, id, "add fail-me: could not capture id from output %q", stdout)
	stdout, _, code = run("set", id, "failed", "oops", "--json")
	require.Equal(t, 0, code, "set failed: unexpected exit code")
	require.JSONEq(t, `{"id":"`+id+`","status":"failed","title":"fail-me","note":"oops"}`, stdout, "set failed --json: expected structured confirmation")
	stdout, _, code = run("task", id)
	require.Equal(t, 0, code, "task after failed: unexpected exit code")
	require.Contains(t, stdout, "failed", "task after failed: expected failed status")
	require.Contains(t, stdout, "oops", "task after failed: expected error reason")

	stdout, _, code = run("set", id, "deleted", "cleanup")
	require.Equal(t, 0, code, "set deleted: unexpected exit code")
	require.Equal(t, "set "+id+" deleted\n", stdout, "set deleted: expected confirmation")
	stdout, _, code = run("status")
	require.Equal(t, 0, code, "status after deleted: unexpected exit code")
	require.Contains(t, stdout, "deleted: 1", "status after deleted: expected deleted: 1")

	stdout, _, code = run("add", "--no-cwd", "task-a")
	require.Equal(t, 0, code, "add task-a: unexpected exit code")
	idA := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, idA)

	stdout, _, code = run("add", "--no-cwd", "task-b")
	require.Equal(t, 0, code, "add task-b: unexpected exit code")
	idB := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, idB)

	stdout, _, code = run("add", "--no-cwd", "task-c")
	require.Equal(t, 0, code, "add task-c: unexpected exit code")
	idC := idRe.FindString(strings.TrimSpace(stdout))
	require.NotEmpty(t, idC)

	stdout, _, code = run("take", "--lease", "30m")
	require.Equal(t, 0, code, "take: unexpected exit code")
	var takeObj map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &takeObj), "take: invalid JSON")
	pickedID, _ := takeObj["id"].(string)
	require.Contains(t, []string{idA, idB, idC}, pickedID, "take: id should be one of the three added tasks")
	require.Equal(t, "doing", takeObj["status"], "take: expected status doing")
	require.NotEmpty(t, takeObj["lease_expires"], "take: expected lease_expires")

	stdout, _, code = run("task", pickedID)
	require.Equal(t, 0, code, "task: unexpected exit code")
	require.Contains(t, stdout, "Events:")
	require.Contains(t, stdout, "Attempts:")

	stdout, _, code = run("status")
	require.Equal(t, 0, code, "status after take: unexpected exit code")
	require.Contains(t, stdout, "todo: 2", "status after take: expected todo: 2")
	require.Contains(t, stdout, "doing: 1", "status after take: expected doing: 1")

	_, stderr, code := run("task", "nonexistent-id")
	require.NotEqual(t, 0, code, "task nonexistent: expected non-zero exit")
	require.Contains(t, stderr, "not found", "task nonexistent: expected 'not found' in stderr")

	_, _, code = run("set", "nonexistent-id", "done")
	require.NotEqual(t, 0, code, "set nonexistent: expected non-zero exit")

	_, err = os.Stat(sqlitePath)
	require.NoError(t, err, "sqlite queue should exist")

	stdout, _, code = run("tasks", "--json")
	require.Equal(t, 0, code, "final tasks --json: unexpected exit code")
	require.NotEmpty(t, readJSONLines(t, stdout, "final tasks --json"))

	stdout, _, code = run("prompt")
	require.Equal(t, 0, code, "prompt: unexpected exit code")
	require.Contains(t, stdout, "Loop Tick")
	require.Contains(t, stdout, "afk take")
	require.Contains(t, stdout, "set <id> done")
	require.Contains(t, stdout, "set <id> failed")
	require.Contains(t, stdout, sqlitePath)
	require.Contains(t, stdout, "`cwd`")

	stdout, _, code = run("prompt", "--task", idA)
	require.Equal(t, 0, code, "prompt --task: unexpected exit code")
	require.Contains(t, stdout, "AFK Task "+idA)
	require.Contains(t, stdout, "task-a")

	promptPath := filepath.Join(queueDir, "loop.md")
	stdout, _, code = run("prompt", "--output", promptPath)
	require.Equal(t, 0, code, "prompt --output: unexpected exit code")
	require.Empty(t, stdout, "prompt --output should not write stdout")
	promptBytes, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	require.Contains(t, string(promptBytes), "Do not pick up another task this tick")
}

func readJSONLines(t *testing.T, stdout, label string) []map[string]any {
	t.Helper()

	var rows []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &obj), "%s line %d is not valid JSON: %q", label, lineNum, line)
		rows = append(rows, obj)
	}
	require.NoError(t, scanner.Err(), "%s scan error", label)
	return rows
}
