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

func TestRunIgnoresHeartbeatAfterTaskFinalized(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, st, queuePath := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "done-early",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "done early",
	}))

	require.NoError(t, runner.Run(ctx, svc, runner.Options{
		Limit:        1,
		Lease:        10 * time.Millisecond,
		WorkerID:     "runner-test",
		ExecTemplate: helperCommand(queuePath, "{{id}}", "done-sleep"),
		QueuePath:    queuePath,
		Stdout:       &bytes.Buffer{},
		Stderr:       &bytes.Buffer{},
	}))

	got, showErr := svc.Show(ctx, "done-early")
	require.NoError(t, showErr)
	require.Equal(t, task.StatusDone, got.Status)
}

func TestRunTask_KillsProcessGroupOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc, st, queuePath := newRunnerService(t)
	require.NoError(t, st.Add(ctx, task.Task{
		ID:      "pgkill",
		Created: time.Now().UTC().Format(time.RFC3339),
		Status:  task.StatusPending,
		Body:    "pgkill",
	}))

	// Temp file the grandchild will append to in a tight loop.
	tmpFile := filepath.Join(t.TempDir(), "grandchild.out")

	// The exec template spawns a grandchild that writes to tmpFile in a loop,
	// then sleeps so the shell (parent) stays alive. The runner should kill
	// the whole process group on cancel, stopping the grandchild too.
	execTemplate := fmt.Sprintf(
		`/bin/sh -c 'while true; do echo x >> %s; sleep 0.05; done &' && sleep 30`,
		tmpFile,
	)

	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx, svc, runner.Options{
			Limit:        1,
			ExecTemplate: execTemplate,
			QueuePath:    queuePath,
			Stdout:       &bytes.Buffer{},
			Stderr:       &bytes.Buffer{},
		})
	}()

	// Wait until the grandchild has written at least once before cancelling.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, statErr := os.Stat(tmpFile)
		if statErr == nil && info.Size() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Cancel the runner context, triggering group kill.
	cancel()

	select {
	case <-runErr:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not return after context cancel")
	}

	// Record file size immediately after kill, then wait and confirm it stopped growing.
	info1, err := os.Stat(tmpFile)
	require.NoError(t, err)
	size1 := info1.Size()

	time.Sleep(200 * time.Millisecond)

	info2, err := os.Stat(tmpFile)
	require.NoError(t, err)
	size2 := info2.Size()

	require.Equal(t, size1, size2, "grandchild process kept writing after group kill — process group was not killed")
}

func newRunnerService(t *testing.T) (*app.Service, *store.SQLiteStore, string) {
	t.Helper()
	ctx := context.Background()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	st, err := store.NewSQLite(ctx, store.Paths{
		SQLitePath: queuePath,
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
