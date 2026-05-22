package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunExecutesRootCommand(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	withArgs(t, "afk", "--queue", queuePath, "prompt", "--discover")

	require.NoError(t, run())
	require.NoFileExists(t, queuePath)
}

func TestRunReturnsCommandError(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	withArgs(t, "afk", "--queue", queuePath, "task", "missing")

	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestRunVersionFlag(t *testing.T) {
	withArgs(t, "afk", "--version")

	require.NoError(t, run())
}

func TestExitCode(t *testing.T) {
	require.Equal(t, 0, exitCode(nil))
	require.Equal(t, 1, exitCode(errors.New("boom")))
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	orig := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = orig })
}
