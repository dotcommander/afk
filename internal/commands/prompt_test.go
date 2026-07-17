package commands

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromptCommandWritesStdoutWithoutCreatingQueue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt"})

	require.NoError(t, root.Execute())
	out := stdout.String()
	require.Contains(t, out, "afk take")
	require.Contains(t, out, "claims the first ready task")
	require.Contains(t, out, "No ready tasks.")
	require.Contains(t, out, queuePath)
	require.Contains(t, out, "Do not read, write, patch, edit, or repair the queue database directly")
	require.NotContains(t, out, "claims the first todo task")
	require.NotContains(t, out, "No todo tasks.")
	require.NoFileExists(t, queuePath)
}

func TestPromptCommandWritesOutputFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.jsonl")
	outputPath := filepath.Join(dir, "loop.md")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--output", outputPath})

	require.NoError(t, root.Execute())
	require.Empty(t, stdout.String())

	body, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	out := string(body)
	require.Contains(t, out, filepath.Join(dir, "tasks.sqlite"))
	require.NotContains(t, out, queuePath)
	require.Contains(t, out, "Do not pick up another task this tick")
}

func TestPromptCmd_ExeNotAbsolute(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt"})

	require.NoError(t, root.Execute())
	out := stdout.String()
	require.Contains(t, out, "afk take")
	require.NotContains(t, out, "/Users/")
	require.NotContains(t, out, "/home/")
}

func TestPromptCommandDiscoverWritesStdoutWithoutCreatingQueue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--discover"})

	require.NoError(t, root.Execute())
	out := stdout.String()
	require.Contains(t, out, "task-discovery contract")
	require.Contains(t, out, "## Happy path")
	require.Contains(t, out, "afk status --summary")
	require.Contains(t, out, "afk take --dry-run --limit 0 --json --full")
	require.Contains(t, out, "targets inspected")
	require.Contains(t, out, "Resolve before promoting")
	require.Contains(t, out, "`accepted`, `prior-art`")
	require.Contains(t, out, "`refuted`")
	require.Contains(t, out, "`residual`")
	require.Contains(t, out, "report `no strong candidate` affirmatively")
	require.Contains(t, out, "changed state requires revalidation")
	require.Contains(t, out, "`prior-art` takes precedence")
	require.Contains(t, out, "require fresh confirmation before enqueue")
	require.Contains(t, out, "dry-run validation result")
	require.Contains(t, out, "<task-body-template>")
	require.Contains(t, out, "afk prompt --discover --full")
	require.NotContains(t, out, "No Shallow Batch Passes")
	require.NotContains(t, out, "afk import")
	require.NotContains(t, out, "afk ready")
	require.NotContains(t, out, "afk pop")
	require.NotContains(t, out, "afk ls")
	require.NoFileExists(t, queuePath)
}

func TestPromptCommandDiscoverFullWritesPolicy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--discover", "--full"})

	require.NoError(t, root.Execute())
	out := stdout.String()
	require.Contains(t, out, "full task-discovery policy")
	require.Contains(t, out, "No Shallow Batch Passes")
	require.Contains(t, out, "Monolith / frankenstein repo pass")
	require.Contains(t, out, "package.json: prefer check, then test, then build")
	// todo-task duplicate-check command added in stub consolidation
	require.Contains(t, out, "afk tasks --status todo --json")
	require.Contains(t, out, "pass its created id with --blocked-by")
	// early-stop rule added in stub consolidation
	require.Contains(t, out, "Early-stop: once you have 1-3 strong")
	// escalation-ladder framing (Level 2 section header)
	require.Contains(t, out, "Level 2 evidence is an escalation ladder, not a fixed checklist")
	require.Contains(t, out, "Resolve before promote")
	require.Contains(t, out, "identify the authority for the disputed behavior")
	require.Contains(t, out, "these are conditional probes, not a fixed checklist")
	require.Contains(t, out, "discovered leads must equal accepted + prior-art + refuted + rejected + residual")
	require.Contains(t, out, "run one adversarial second pass")
	require.Contains(t, out, "do not ask an enqueue question")
	require.Contains(t, out, "Prior-art takes precedence")
	require.Contains(t, out, "require fresh confirmation before mutation")
	require.NotContains(t, out, "Use blocked_by")
	require.NotContains(t, out, "afk import")
	require.NotContains(t, out, "afk ready")
	require.NotContains(t, out, "afk pop")
	require.NotContains(t, out, "afk ls")
	require.NoFileExists(t, queuePath)
}

func TestPromptCommandDiscoverRejectsPathArgument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--discover", filepath.Join(dir, "project")})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "prompt --discover does not accept path arguments")
	require.Empty(t, stdout.String())
	require.NoFileExists(t, queuePath)
}

func TestPromptCommandDiscoverConflictsWithTask(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--discover", "--task", "1"})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--task and --discover are mutually exclusive")
	require.Empty(t, stdout.String())
	require.NoFileExists(t, queuePath)
}

func TestPromptCommandFullRequiresDiscover(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--full"})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--full requires --discover")
	require.Empty(t, stdout.String())
	require.NoFileExists(t, queuePath)
}

func TestWorkNotesWithOldAFKCommandsAreArchived(t *testing.T) {
	t.Parallel()

	workDir := filepath.Join("..", "..", ".work")
	oldNeedles := []string{
		"afk pop",
		"afk done",
		"afk fail",
		"afk run",
		"afk import",
		"afk ready",
		"blocked_by",
		"`pending`",
		"`working`",
	}
	var offenders []string
	err := filepath.WalkDir(workDir, func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		text := string(body)
		for _, needle := range oldNeedles {
			if strings.Contains(text, needle) && !strings.HasPrefix(text, "# Archived Planning Note\n") {
				offenders = append(offenders, path)
				break
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, offenders)
}

func testDeps(stdout *bytes.Buffer) *Deps {
	return testDepsWithWriters(stdout, &bytes.Buffer{})
}
