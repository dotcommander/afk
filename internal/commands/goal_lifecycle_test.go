package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeStubAuditAgent writes an executable script that prints the given marker
// ("<approved/>" or "<disapproved/>") to stdout, ignoring its prompt argument.
func writeStubAuditAgent(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "stub-audit.sh")
	body := "#!/bin/sh\nprintf '%s\\n' '" + marker + "'\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	return script
}

// goalTaskRow is the minimal shape parsed from `afk tasks --json` JSONL output.
type goalTaskRow struct {
	ID      string `json:"id"`
	GroupID string `json:"group_id"`
	Body    string `json:"body"`
	Status  string `json:"status"`
}

// parseGoalTaskRows parses the JSONL output of `afk tasks --json` into a slice
// of goalTaskRow, skipping blank lines.
func parseGoalTaskRows(t *testing.T, out string) []goalTaskRow {
	t.Helper()
	var rows []goalTaskRow
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row goalTaskRow
		require.NoError(t, json.Unmarshal([]byte(line), &row), "parsing task line: %s", line)
		rows = append(rows, row)
	}
	return rows
}

// TestGoalLifecycleApprovePath proves the full approve path: goal inserts 3
// tasks, goal status reflects the objective and todo count, and audit approve
// leaves the task status unchanged.
func TestGoalLifecycleApprovePath(t *testing.T) {
	t.Parallel()

	const objective = "add CSV export to the report command"
	const firstTaskBody = "add --csv flag to the report command"

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	setup := writeStubSetupAgent(t)
	approveAudit := writeStubAuditAgent(t, "<approved/>")

	execCmd := func(stdin string, args ...string) (string, string, error) {
		t.Helper()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		d := testDepsWithWriters(stdout, stderr)
		if stdin != "" {
			d.Stdin = strings.NewReader(stdin)
		}
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		err := root.Execute()
		return stdout.String(), stderr.String(), err
	}

	// Step 1: run goal with interactive approval "yes".
	_, stderr, err := execCmd("yes\n", "goal", "--setup-command", setup, objective)
	require.NoError(t, err, "stderr: %s", stderr)

	// Step 2: list tasks; find task with firstTaskBody; require 3 tasks total.
	tasksOut, _, err := execCmd("", "tasks", "--json")
	require.NoError(t, err)
	rows := parseGoalTaskRows(t, tasksOut)
	require.Len(t, rows, 3, "expected 3 inserted tasks, got: %s", tasksOut)

	var targetID, goalID string
	for _, r := range rows {
		if r.Body == firstTaskBody {
			targetID = r.ID
			goalID = r.GroupID
		}
	}
	require.NotEmpty(t, targetID, "task with body %q not found in: %s", firstTaskBody, tasksOut)
	require.NotEmpty(t, goalID, "group_id not set on task %s", targetID)

	// Step 3: goal status — contains the contract outcome and "todo".
	// The stored objective is the contract's "outcome" field, not the CLI arg.
	statusOut, _, err := execCmd("", "goal", "status", goalID)
	require.NoError(t, err)
	require.Contains(t, statusOut, "report command supports CSV export") // contractBlock outcome
	require.Contains(t, statusOut, "todo")

	// Step 4: audit approve — output contains approved:true and disapproved:false.
	auditOut, _, err := execCmd("", "goal", "audit", targetID, "--audit-command", approveAudit)
	require.NoError(t, err)
	require.Contains(t, auditOut, `"approved":true`)
	require.Contains(t, auditOut, `"disapproved":false`)

	// Step 5: task status still todo after approve (approve must not change status).
	tasksOut2, _, err := execCmd("", "tasks", "--json")
	require.NoError(t, err)
	rows2 := parseGoalTaskRows(t, tasksOut2)
	for _, r := range rows2 {
		if r.ID == targetID {
			require.Equal(t, "todo", r.Status, "approve must not change task status")
			return
		}
	}
	t.Fatalf("task %s not found after audit approve", targetID)
}

// TestGoalLifecycleDisapproveRequeues proves that audit disapprove moves a
// doing task back to todo.
func TestGoalLifecycleDisapproveRequeues(t *testing.T) {
	t.Parallel()

	const objective = "add CSV export to the report command"
	const firstTaskBody = "add --csv flag to the report command"

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	setup := writeStubSetupAgent(t)
	disapproveAudit := writeStubAuditAgent(t, "<disapproved/>")

	execCmd := func(stdin string, args ...string) (string, string, error) {
		t.Helper()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		d := testDepsWithWriters(stdout, stderr)
		if stdin != "" {
			d.Stdin = strings.NewReader(stdin)
		}
		root := NewRoot(d, "test")
		root.SetArgs(append([]string{"--queue", queuePath}, args...))
		err := root.Execute()
		return stdout.String(), stderr.String(), err
	}

	// Step 1: insert via goal with interactive approval "yes".
	_, stderr, err := execCmd("yes\n", "goal", "--setup-command", setup, objective)
	require.NoError(t, err, "stderr: %s", stderr)

	// Step 2: recover task 1 (firstTaskBody) ID.
	tasksOut, _, err := execCmd("", "tasks", "--json")
	require.NoError(t, err)
	rows := parseGoalTaskRows(t, tasksOut)
	require.Len(t, rows, 3, "expected 3 inserted tasks, got: %s", tasksOut)

	var targetID string
	for _, r := range rows {
		if r.Body == firstTaskBody {
			targetID = r.ID
		}
	}
	require.NotEmpty(t, targetID, "task with body %q not found in: %s", firstTaskBody, tasksOut)

	// Step 3: move task to doing (non-terminal; no note required).
	setOut, setErr, err := execCmd("", "set", targetID, "doing")
	require.NoError(t, err, "set doing stdout=%s stderr=%s", setOut, setErr)

	// Verify it is now doing.
	tasksOut2, _, err := execCmd("", "tasks", "--json")
	require.NoError(t, err)
	rows2 := parseGoalTaskRows(t, tasksOut2)
	var doingStatus string
	for _, r := range rows2 {
		if r.ID == targetID {
			doingStatus = r.Status
		}
	}
	require.Equal(t, "doing", doingStatus, "task should be doing after set doing")

	// Step 4: audit disapprove.
	auditOut, _, err := execCmd("", "goal", "audit", targetID, "--audit-command", disapproveAudit)
	require.NoError(t, err)
	require.Contains(t, auditOut, `"disapproved":true`)

	// Step 5: task should be back to todo (requeued).
	tasksOut3, _, err := execCmd("", "tasks", "--json")
	require.NoError(t, err)
	rows3 := parseGoalTaskRows(t, tasksOut3)
	var requeuedStatus string
	for _, r := range rows3 {
		if r.ID == targetID {
			requeuedStatus = r.Status
		}
	}
	require.Equal(t, "todo", requeuedStatus, "disapprove must requeue task to todo")
}
