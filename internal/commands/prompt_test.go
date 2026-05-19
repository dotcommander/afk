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
	require.Contains(t, out, queuePath)
	require.Contains(t, out, "Do not pick up another task this tick")
}

func testDeps(stdout *bytes.Buffer) *Deps {
	return testDepsWithWriters(stdout, &bytes.Buffer{})
}
