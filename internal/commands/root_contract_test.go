package commands

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootMetadataContract(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no args", want: "Usage: afk <command>"},
		{name: "root help", args: []string{"--help"}, want: "Usage: afk <command>"},
		{name: "hidden help", args: []string{"__help", "take"}, want: "Usage: afk <command>"},
		{name: "version", args: []string{"--version"}, want: "afk version test"},
		{name: "version shorthand", args: []string{"-v"}, want: "afk version test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			d := testDepsWithWriters(&stdout, &stderr)
			require.NoError(t, Execute(context.Background(), append([]string{"--queue", queuePath}, tc.args...), &stdout, &stderr, d, "test"))
			require.Contains(t, stdout.String(), tc.want)
			require.NoFileExists(t, queuePath)
		})
	}
}

func TestRootHelpPreservesParentCommandsAndGoalSurface(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	d := testDepsWithWriters(&stdout, &stderr)
	require.NoError(t, Execute(context.Background(), []string{"--help"}, &stdout, &stderr, d, "test"))
	for _, command := range []string{"goal", "gate", "checkpoint", "artifact"} {
		require.Contains(t, stdout.String(), command)
	}
	require.NotContains(t, stdout.String(), "<extra>")

	stdout.Reset()
	require.NoError(t, Execute(context.Background(), []string{"goal", "--help"}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), "Usage: afk goal <objective>")
	require.NotContains(t, stdout.String(), "goal create")
	require.Contains(t, stdout.String(), "--setup-command")
	require.NotContains(t, stdout.String(), "<extra>")
}

func TestGoalFlagsRemainLocallyOwned(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"goal", "status", "missing", "--dry-run"},
		{"goal", "status", "missing", "--setup-command", "x"},
		{"goal", "audit", "missing", "--cwd", "x"},
	} {
		var stdout, stderr bytes.Buffer
		d := testDepsWithWriters(&stdout, &stderr)
		err := Execute(context.Background(), args, &stdout, &stderr, d, "test")
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown flag")
	}
}

func TestLegacyPermissiveCommandsIgnoreExtraArgs(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"tasks", "status", "take", "snapshot", "reap"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			d := testDepsWithWriters(&stdout, &stderr)
			queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
			require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, command, "extra"}, &stdout, &stderr, d, "test"))
		})
	}

	var stdout, stderr bytes.Buffer
	d := testDepsWithWriters(&stdout, &stderr)
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	err := Execute(context.Background(), []string{"--queue", queuePath, "loop", "extra"}, &stdout, &stderr, d, "test")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "unexpected argument")
}

func TestPermissiveCompatibilityArgsStayHiddenFromHelp(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"tasks", "status", "take", "snapshot", "serve", "reap", "loop"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			d := testDepsWithWriters(&stdout, &stderr)
			require.NoError(t, Execute(context.Background(), []string{command, "--help"}, &stdout, &stderr, d, "test"))
			require.NotContains(t, stdout.String(), "<extra>")
			require.NotContains(t, stdout.String(), "Arguments:")
		})
	}
}

func TestRootRejectsCompletionAndPublicHelp(t *testing.T) {
	for _, command := range []string{"completion", "help"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			d := testDepsWithWriters(&stdout, &stderr)
			err := Execute(context.Background(), []string{command}, &stdout, &stderr, d, "test")
			require.Error(t, err)
			require.Contains(t, err.Error(), "unknown command")
			require.Empty(t, stdout.String())
			require.Empty(t, stderr.String())
		})
	}
}
