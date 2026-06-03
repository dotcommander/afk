package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// contractBlock is the fixed <contract> payload printed by the stub setup
// agent. Its field values match testdata/goal_dry_run.golden so the parsed
// contract round-trips through output.WriteJSONLine to the golden bytes.
const contractBlock = `<contract>
{
  "outcome": "report command supports CSV export",
  "done_criteria": [
    "` + "`afk report --csv`" + ` emits valid RFC 4180 CSV",
    "existing JSON output unchanged",
    "unit test covers the CSV encoder"
  ],
  "must_do": [
    "add a --csv flag to the report command",
    "route output through a dedicated CSV encoder"
  ],
  "avoid": [
    "breaking the default JSON output",
    "writing CSV by string concatenation"
  ],
  "philosophy": "smallest change that satisfies the done criteria; reuse existing output plumbing",
  "tasks": [
    "add --csv flag to the report command",
    "implement the CSV encoder for report rows",
    "add a unit test asserting valid CSV output"
  ]
}
</contract>`

// writeStubSetupAgent writes an executable script that prints contractBlock to
// stdout, ignoring its prompt argument, and returns its path. This is the
// deterministic stand-in for the real setup agent so the goal command's
// compile → parse → render path runs end-to-end without an LLM.
func writeStubSetupAgent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "stub-setup.sh")
	body := "#!/bin/sh\ncat <<'EOF'\n" + contractBlock + "\nEOF\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	return script
}

// TestGoalDryRun verifies `afk goal --dry-run --json` prints the compiled
// contract as JSON matching the golden fixture and writes no tasks.
func TestGoalDryRun(t *testing.T) {
	t.Parallel()
	assertGoalDryRunGolden(t)
}

// TestGoalCommand is the spec's named acceptance test for the dry-run golden.
func TestGoalCommand(t *testing.T) {
	t.Parallel()
	assertGoalDryRunGolden(t)
}

func assertGoalDryRunGolden(t *testing.T) {
	t.Helper()

	golden, err := os.ReadFile(filepath.Join("testdata", "goal_dry_run.golden"))
	require.NoError(t, err)

	setup := writeStubSetupAgent(t)
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	root := NewRoot(d, "test")
	root.SetArgs([]string{
		"--queue", queuePath,
		"goal", "--dry-run", "--json",
		"--setup-command", setup,
		"add CSV export to the report command",
	})
	require.NoError(t, root.Execute(), "stderr: %s", stderr.String())
	require.Equal(t, string(golden), stdout.String())

	// Dry-run must not queue anything.
	tasksOut := &bytes.Buffer{}
	d2 := testDepsWithWriters(tasksOut, &bytes.Buffer{})
	listRoot := NewRoot(d2, "test")
	listRoot.SetArgs([]string{"--queue", queuePath, "tasks", "--json"})
	require.NoError(t, listRoot.Execute())
	require.NotContains(t, tasksOut.String(), "add --csv flag to the report command")
}

// TestGoalApproval verifies the interactive approval gate: "no" declines with a
// nonzero exit and no tasks; "yes" inserts the contract's tasks.
func TestGoalApproval(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, answer string) (queuePath string, stdout, stderr *bytes.Buffer, execErr error) {
		t.Helper()
		setup := writeStubSetupAgent(t)
		queuePath = filepath.Join(t.TempDir(), "tasks.sqlite")
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
		d := testDepsWithWriters(stdout, stderr)
		d.Stdin = strings.NewReader(answer)
		root := NewRoot(d, "test")
		root.SetArgs([]string{
			"--queue", queuePath,
			"goal",
			"--setup-command", setup,
			"add CSV export to the report command",
		})
		return queuePath, stdout, stderr, root.Execute()
	}

	t.Run("decline", func(t *testing.T) {
		t.Parallel()
		queuePath, _, stderr, err := run(t, "no\n")
		require.Error(t, err)
		require.Contains(t, stderr.String(), "goal declined by user")

		tasksOut := &bytes.Buffer{}
		d := testDepsWithWriters(tasksOut, &bytes.Buffer{})
		listRoot := NewRoot(d, "test")
		listRoot.SetArgs([]string{"--queue", queuePath, "tasks", "--json"})
		require.NoError(t, listRoot.Execute())
		require.NotContains(t, tasksOut.String(), "add --csv flag to the report command")
	})

	t.Run("approve", func(t *testing.T) {
		t.Parallel()
		queuePath, _, stderr, err := run(t, "yes\n")
		require.NoError(t, err, "stderr: %s", stderr.String())

		tasksOut := &bytes.Buffer{}
		d := testDepsWithWriters(tasksOut, &bytes.Buffer{})
		listRoot := NewRoot(d, "test")
		listRoot.SetArgs([]string{"--queue", queuePath, "tasks", "--json"})
		require.NoError(t, listRoot.Execute())
		require.Contains(t, tasksOut.String(), "add --csv flag to the report command")
	})
}
