package runner_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/runner"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestRunFailsInvalidClaimWithoutExecutingCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.NewSQLite(ctx, store.Paths{
		SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite"),
		JSONLPath:  filepath.Join(t.TempDir(), "tasks.jsonl"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "bad",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "pick my nose",
	}))

	svc := app.NewService(st, func() time.Time { return time.Now().UTC() })
	var stdout bytes.Buffer
	err = runner.Run(ctx, svc, runner.Options{
		Limit:        1,
		ExecTemplate: "echo should-not-run",
		Stdout:       &stdout,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, task.ErrInvalidTask), "got %v", err)
	require.NotContains(t, stdout.String(), "should-not-run")

	got, err := svc.Show(ctx, "bad")
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status)
	require.Contains(t, got.Error, "invalid task")
	require.False(t, strings.Contains(stdout.String(), "running bad"))
}

func TestDryRunBoundsRenderedCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.NewSQLite(ctx, store.Paths{
		SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite"),
		JSONLPath:  filepath.Join(t.TempDir(), "tasks.jsonl"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "big",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    strings.Repeat("x", 2000),
	}))

	svc := app.NewService(st, func() time.Time { return time.Now().UTC() })
	var stdout bytes.Buffer
	require.NoError(t, runner.Run(ctx, svc, runner.Options{
		DryRun:       true,
		Limit:        1,
		ExecTemplate: "echo {{body}}",
		Stdout:       &stdout,
	}))
	require.Contains(t, stdout.String(), "…")
	require.NotContains(t, stdout.String(), strings.Repeat("x", 1500))
}
