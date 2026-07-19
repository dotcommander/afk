package commands

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGateCommands(t *testing.T) {
	t.Parallel()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	var stdout, stderr bytes.Buffer
	d := testDepsWithWriters(&stdout, &stderr)

	// Create a task first
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "add", "--no-cwd", "gate target task"}, &stdout, &stderr, d, "test"))
	id := strings.TrimSpace(stdout.String())
	require.NotEmpty(t, id)

	stdout.Reset()
	stderr.Reset()

	// Add gate via CLI
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "gate", "add", id, "qa-review"}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), "gate add "+id+" qa-review")

	stdout.Reset()
	stderr.Reset()

	// Satisfy gate via CLI
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "gate", "satisfy", id, "qa-review"}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), "gate satisfy "+id+" qa-review")
}

func TestRelateCommand(t *testing.T) {
	t.Parallel()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	var stdout, stderr bytes.Buffer
	d := testDepsWithWriters(&stdout, &stderr)

	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "add", "--no-cwd", "task 1"}, &stdout, &stderr, d, "test"))
	id1 := strings.TrimSpace(stdout.String())

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "add", "--no-cwd", "task 2"}, &stdout, &stderr, d, "test"))
	id2 := strings.TrimSpace(stdout.String())

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "relate", id1, id2, "--type", "relates"}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), "Relation added: "+id1+" relates "+id2)
}

func TestCheckpointAndArtifactCommands(t *testing.T) {
	t.Parallel()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	var stdout, stderr bytes.Buffer
	d := testDepsWithWriters(&stdout, &stderr)

	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "add", "--no-cwd", "record target task"}, &stdout, &stderr, d, "test"))
	id := strings.TrimSpace(stdout.String())

	// Checkpoint Add & List
	stdout.Reset()
	stderr.Reset()
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "checkpoint", "add", id, `{"step":1}`, "--kind", "progress", "--key", "step1"}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), `"kind":"progress"`)

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "checkpoint", "list", id}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), `"key":"step1"`)

	// Artifact Add & List
	stdout.Reset()
	stderr.Reset()
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "artifact", "add", id, "/tmp/out.txt", "--content-type", "text/plain"}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), `"/tmp/out.txt"`)

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, Execute(context.Background(), []string{"--queue", queuePath, "artifact", "list", id}, &stdout, &stderr, d, "test"))
	require.Contains(t, stdout.String(), `"/tmp/out.txt"`)
}

func TestImportCommandValidation(t *testing.T) {
	t.Parallel()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	var stdout, stderr bytes.Buffer
	d := testDepsWithWriters(&stdout, &stderr)

	// Neither --dry-run nor --apply set
	err := Execute(context.Background(), []string{"--queue", queuePath, "import", "vybe", "/tmp/nonexistent"}, &stdout, &stderr, d, "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one of --dry-run or --apply is required")

	// Both --dry-run and --apply set
	err = Execute(context.Background(), []string{"--queue", queuePath, "import", "vybe", "/tmp/nonexistent", "--dry-run", "--apply"}, &stdout, &stderr, d, "test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one of --dry-run or --apply is required")
}
