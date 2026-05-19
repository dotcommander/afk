package runner_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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

func TestRunRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _, _ := newRunnerService(t)

	err := runner.Run(ctx, svc, runner.Options{Workers: 2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--workers > 1")

	err = runner.Run(ctx, svc, runner.Options{Limit: -1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--limit must be non-negative")

	err = runner.Run(ctx, svc, runner.Options{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--exec is required")
}

func TestDryRunWithoutExecTemplateShowsPreviewHint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, st, _ := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "ready",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "ready",
	}))

	var stdout bytes.Buffer
	require.NoError(t, runner.Run(ctx, svc, runner.Options{
		DryRun: true,
		Limit:  1,
		Stdout: &stdout,
	}))
	require.Contains(t, stdout.String(), "(set --exec to preview command)")
}

func TestRunCommandFinalizesTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, st, queuePath := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "done",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "done",
	}))

	var stdout bytes.Buffer
	require.NoError(t, runner.Run(ctx, svc, runner.Options{
		Limit:        1,
		ExecTemplate: helperCommand(queuePath, "{{id}}", "done"),
		QueuePath:    queuePath,
		Stdout:       &stdout,
	}))

	got, err := svc.Show(ctx, "done")
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)
	require.Contains(t, stdout.String(), "running done")
}

func TestRunCommandExitFailureMarksTaskFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, st, queuePath := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "fail",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "fail",
	}))

	err := runner.Run(ctx, svc, runner.Options{
		Limit:        1,
		ExecTemplate: helperCommand(queuePath, "{{id}}", "exit1"),
		QueuePath:    queuePath,
		Stdout:       &bytes.Buffer{},
		Stderr:       &bytes.Buffer{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "runner: command for fail")

	got, showErr := svc.Show(ctx, "fail")
	require.NoError(t, showErr)
	require.Equal(t, task.StatusFailed, got.Status)
	require.Contains(t, got.Error, "runner command failed")
}

func TestRunHeartbeatRecordsEventWhileCommandRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, st, queuePath := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "heartbeat",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "heartbeat",
	}))

	require.NoError(t, runner.Run(ctx, svc, runner.Options{
		Limit:        1,
		Lease:        10 * time.Millisecond,
		WorkerID:     "runner-test",
		ExecTemplate: helperCommand(queuePath, "{{id}}", "sleep-done"),
		QueuePath:    queuePath,
		Stdout:       &bytes.Buffer{},
		Stderr:       &bytes.Buffer{},
	}))

	events, err := st.Events(ctx, "heartbeat")
	require.NoError(t, err)
	var heartbeat bool
	for _, event := range events {
		if event.Type == task.EventHeartbeat {
			heartbeat = true
			require.Equal(t, "runner-test", event.Message)
		}
	}
	require.True(t, heartbeat, "expected at least one heartbeat event")
}

func TestRunMaxDurationCancelsCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, st, queuePath := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "slow",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "slow",
	}))

	err := runner.Run(ctx, svc, runner.Options{
		Limit:        1,
		MaxDuration:  50 * time.Millisecond,
		ExecTemplate: helperCommand(queuePath, "{{id}}", "sleep-done"),
		QueuePath:    queuePath,
		Stdout:       &bytes.Buffer{},
		Stderr:       &bytes.Buffer{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "runner: command for slow")

	got, showErr := svc.Show(context.Background(), "slow")
	require.NoError(t, showErr)
	require.Equal(t, task.StatusFailed, got.Status)
}

func TestRunHeartbeatErrorStopsRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, st, queuePath := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "done-early",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "done early",
	}))

	err := runner.Run(ctx, svc, runner.Options{
		Limit:        1,
		Lease:        10 * time.Millisecond,
		WorkerID:     "runner-test",
		ExecTemplate: helperCommand(queuePath, "{{id}}", "done-sleep"),
		QueuePath:    queuePath,
		Stdout:       &bytes.Buffer{},
		Stderr:       &bytes.Buffer{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "runner: heartbeat done-early")

	got, showErr := svc.Show(ctx, "done-early")
	require.NoError(t, showErr)
	require.Equal(t, task.StatusDone, got.Status)
}

func newRunnerService(t *testing.T) (*app.Service, *store.SQLiteStore, string) {
	t.Helper()
	ctx := context.Background()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	st, err := store.NewSQLite(ctx, store.Paths{
		SQLitePath: queuePath,
		JSONLPath:  filepath.Join(t.TempDir(), "tasks.jsonl"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	return app.NewService(st, func() time.Time { return time.Now().UTC() }), st, queuePath
}

func helperCommand(queuePath, id, action string) string {
	return fmt.Sprintf(
		"AFK_RUNNER_HELPER=1 %s -test.run=TestRunnerHelperProcess -- %s %s %s",
		os.Args[0],
		queuePath,
		id,
		action,
	)
}

func TestRunnerHelperProcess(_ *testing.T) {
	if os.Getenv("AFK_RUNNER_HELPER") != "1" {
		return
	}
	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || len(args) != sep+4 {
		os.Exit(2)
	}

	queuePath, id, action := args[sep+1], args[sep+2], args[sep+3]
	if action == "exit1" {
		os.Exit(1)
	}

	ctx := context.Background()
	st, err := store.NewSQLite(ctx, store.Paths{SQLitePath: queuePath})
	if err != nil {
		os.Exit(2)
	}
	svc := app.NewService(st, func() time.Time { return time.Now().UTC() })

	switch action {
	case "done":
		err = svc.Done(ctx, id)
	case "done-sleep":
		err = svc.Done(ctx, id)
		if err == nil {
			time.Sleep(1200 * time.Millisecond)
		}
	case "sleep-done":
		time.Sleep(1200 * time.Millisecond)
		err = svc.Done(ctx, id)
	default:
		_ = st.Close()
		os.Exit(2)
	}
	if err != nil {
		_ = st.Close()
		os.Exit(2)
	}
	if err := st.Close(); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
