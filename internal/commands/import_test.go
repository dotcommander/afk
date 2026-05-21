package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// validBody is a task body that satisfies validateImportBatch's section checks.
// It must contain exactly the lines "Success:" and "Verify:" each on their own line.
// The JSON-escaped form (\\n) is safe to embed inside a JSON string literal.
const validBody = `do the work\nSuccess:\ndone\nVerify:\nchecked`

// runImport executes `afk import` against a fresh queue at queuePath, feeding
// input on stdin, and returns stdout and the cobra error (may be *ExitError).
func runImport(t *testing.T, queuePath, input string) (string, error) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	d.Stdin = strings.NewReader(input) // import reads d.Stdin, not cobra's stdin
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "import"})
	err := root.Execute()
	return stdout.String(), err
}

// listTasks returns all tasks in the queue at queuePath via a `afk ls --json`
// round-trip so we stay fully in-process with no store construction in the test.
// It returns the count of tasks in the store, inferred from NDJSON lines.
func requireExitCode(t *testing.T, err error, code int) {
	t.Helper()
	var exitErr *ExitError
	require.True(t, errors.As(err, &exitErr), "error must be *ExitError, got %T: %v", err, err)
	require.Equal(t, code, exitErr.Code)
}

func countTasks(t *testing.T, queuePath string) int {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "ls", "--json"})
	require.NoError(t, root.Execute(), "ls stderr: %s", stderr.String())
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return 0
	}
	return strings.Count(out, "\n") + 1
}

// dependenciesOf returns all dependency rows for taskID via `afk deps ls --json`.
func dependenciesOf(t *testing.T, queuePath, taskID string) []task.Dependency {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "deps", "ls", taskID, "--json"})
	require.NoError(t, root.Execute(), "deps ls stderr: %s", stderr.String())
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return nil
	}
	var deps []task.Dependency
	require.NoError(t, json.Unmarshal([]byte(out), &deps))
	return deps
}

// parseNDJSON decodes each non-empty line of s as a JSON object into a slice of maps.
func parseNDJSON(t *testing.T, s string) []map[string]string {
	t.Helper()
	var results []map[string]string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]string
		require.NoError(t, json.Unmarshal([]byte(line), &m), "bad NDJSON line: %s", line)
		results = append(results, m)
	}
	require.NoError(t, sc.Err())
	return results
}

func TestImport(t *testing.T) {
	t.Parallel()

	t.Run("happy_path", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		input := `{"tasks":[
			{"slug":"alpha","body":"` + validBody + `","tags":["spec:demo"]},
			{"slug":"beta","body":"` + validBody + `","tags":["spec:demo"],"blocked_by":["alpha"]}
		]}`

		out, err := runImport(t, queuePath, input)
		require.NoError(t, err)

		lines := parseNDJSON(t, out)
		require.Len(t, lines, 2, "expected 2 NDJSON output lines")
		require.Equal(t, "alpha", lines[0]["slug"])
		require.NotEmpty(t, lines[0]["id"])
		require.Equal(t, "beta", lines[1]["slug"])
		require.NotEmpty(t, lines[1]["id"])

		require.Equal(t, 2, countTasks(t, queuePath), "store should contain 2 tasks")

		// beta must depend on alpha.
		betaID := lines[1]["id"]
		alphaID := lines[0]["id"]
		deps := dependenciesOf(t, queuePath, betaID)
		require.Len(t, deps, 1)
		require.Equal(t, betaID, deps[0].TaskID)
		require.Equal(t, alphaID, deps[0].DependsOnID)
	})

	t.Run("empty_doc_exit_1", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		_, err := runImport(t, queuePath, `{"tasks":[]}`)
		require.Error(t, err)

		requireExitCode(t, err, 1)

		require.Equal(t, 0, countTasks(t, queuePath), "no tasks should be written on error")
	})

	t.Run("invalid_json_exit_1", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		_, err := runImport(t, queuePath, `not json{`)
		require.Error(t, err)

		requireExitCode(t, err, 1)

		require.Equal(t, 0, countTasks(t, queuePath))
	})

	t.Run("invalid_task_exit_1", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		input := `{"tasks":[{"slug":"bad","body":"pick my nose\nSuccess:\ndone\nVerify:\nchecked"}]}`
		_, err := runImport(t, queuePath, input)
		require.Error(t, err)

		requireExitCode(t, err, 1)
		require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
		require.Equal(t, 0, countTasks(t, queuePath))
	})

	t.Run("invalid_priority_exit_1", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		input := `{"tasks":[{"slug":"bad-priority","body":"` + validBody + `","priority":"hihg"}]}`
		_, err := runImport(t, queuePath, input)
		require.Error(t, err)

		requireExitCode(t, err, 1)
		require.True(t, errors.Is(err, task.ErrInvalidPriority), "got %v", err)
		require.Equal(t, 0, countTasks(t, queuePath))
	})

	t.Run("dependency_cycle_exit_2", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		input := `{"tasks":[
			{"slug":"a","body":"` + validBody + `","blocked_by":["b"]},
			{"slug":"b","body":"` + validBody + `","blocked_by":["a"]}
		]}`

		_, err := runImport(t, queuePath, input)
		require.Error(t, err)

		requireExitCode(t, err, 2)

		require.True(t, errors.Is(err, store.ErrDependencyCycle),
			"error chain must contain store.ErrDependencyCycle; got: %v", err)

		require.Equal(t, 0, countTasks(t, queuePath), "atomicity: no tasks on cycle error")
	})

	t.Run("duplicate_spec_tag_exit_3", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		// First import succeeds.
		first := `{"tasks":[{"slug":"orig","body":"` + validBody + `","tags":["spec:dup"]}]}`
		_, err := runImport(t, queuePath, first)
		require.NoError(t, err, "first import should succeed")
		require.Equal(t, 1, countTasks(t, queuePath))

		// Second import with same spec tag must be rejected.
		second := `{"tasks":[{"slug":"dupe","body":"` + validBody + `","tags":["spec:dup"]}]}`
		_, err = runImport(t, queuePath, second)
		require.Error(t, err)

		requireExitCode(t, err, 3)

		var dupErr *app.ErrDuplicateSpec
		require.True(t, errors.As(err, &dupErr),
			"error chain must contain *app.ErrDuplicateSpec; got: %v", err)

		// Original task remains; second batch rolled back.
		require.Equal(t, 1, countTasks(t, queuePath), "only original task should remain")
	})

	t.Run("atomicity_no_partial_writes", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		queuePath := filepath.Join(dir, "queue.db")

		// "bad" has a self-cycle (blocked_by itself), which resolveDeps will
		// catch as an unknown slug — OR BulkAdd will catch as a self-edge.
		// Either way the whole batch must roll back.
		input := `{"tasks":[
			{"slug":"good1","body":"` + validBody + `"},
			{"slug":"good2","body":"` + validBody + `"},
			{"slug":"good3","body":"` + validBody + `"},
			{"slug":"bad","body":"` + validBody + `","blocked_by":["bad"]}
		]}`

		_, err := runImport(t, queuePath, input)
		require.Error(t, err, "import with self-cycle must fail")

		require.Equal(t, 0, countTasks(t, queuePath),
			"atomicity: all 4 tasks must be absent when the batch fails")
	})
}
