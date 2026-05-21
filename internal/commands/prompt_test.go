package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromptCommandWritesStdoutWithoutCreatingQueue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt"})

	require.NoError(t, root.Execute())
	out := stdout.String()
	require.Contains(t, out, "afk pop")
	require.Contains(t, out, queuePath)
	require.Contains(t, out, "Do not read, write, patch, edit, or repair the queue database directly")
	require.NoFileExists(t, queuePath)
}

func TestPromptCommandWritesOutputFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.jsonl")
	outputPath := filepath.Join(dir, "loop.md")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := NewRoot(d, "test")
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
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt"})

	require.NoError(t, root.Execute())
	out := stdout.String()
	require.Contains(t, out, "afk pop")
	require.NotContains(t, out, "/Users/")
	require.NotContains(t, out, "/home/")
}

func TestPromptCommandDiscoverWritesStdoutWithoutCreatingQueue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--discover"})

	require.NoError(t, root.Execute())
	out := stdout.String()
	require.Contains(t, out, "No Shallow Batch Passes")
	require.Contains(t, out, "package.json: prefer check, then test, then build")
	require.NoFileExists(t, queuePath)
}

func TestPromptCommandDiscoverRejectsPathArgument(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queuePath := filepath.Join(dir, "tasks.sqlite")
	var stdout bytes.Buffer
	d := testDeps(&stdout)
	root := NewRoot(d, "test")
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
	root := NewRoot(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "prompt", "--discover", "--task", "1"})

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--task and --discover are mutually exclusive")
	require.Empty(t, stdout.String())
	require.NoFileExists(t, queuePath)
}

func testDeps(stdout *bytes.Buffer) *Deps {
	return testDepsWithWriters(stdout, &bytes.Buffer{})
}
