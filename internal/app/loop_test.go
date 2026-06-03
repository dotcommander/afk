package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// minCfg returns a LoopConfig suitable for fast unit tests: zero cooldown,
// no heartbeat, small lease.
// trueCmd / falseCmd / sleepCmd use absolute paths because exec.Command does
// not search PATH; macOS places these under /usr/bin (true/false) and /bin (sleep).
const (
	trueCmd  = "/usr/bin/true"
	falseCmd = "/usr/bin/false"
	sleepCmd = "/bin/sleep"
)

func minCfg(command string) app.LoopConfig {
	return app.LoopConfig{
		Command:                command,
		PromptTemplate:         `{{.ID}}`,
		TaskTimeout:            5 * time.Second,
		Cooldown:               0,
		Lease:                  time.Minute,
		HeartbeatInterval:      0,
		MaxConsecutiveFailures: 0,
	}
}

// addReady seeds n ready tasks.
func addReady(t *testing.T, svc *app.Service, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, n)
	for i := range n {
		id, err := svc.AddWithOptions(ctx, task.AddOptions{Body: "loop test task"})
		require.NoError(t, err)
		ids[i] = id
	}
	return ids
}

// runLoop drives RunLoop and collects emitted results.
func runLoop(t *testing.T, svc *app.Service, cfg app.LoopConfig, opts app.LoopOptions) ([]app.LoopResult, error) {
	t.Helper()
	var results []app.LoopResult
	err := svc.RunLoop(
		context.Background(), cfg, opts,
		nil, nil,
		func(r app.LoopResult) error {
			results = append(results, r)
			return nil
		},
	)
	return results, err
}

// 1. Success path: tasks driven to done; emit receives Status="done".
func TestRunLoopSuccess(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ctx := context.Background()
	ids := addReady(t, svc, 2)

	results, err := runLoop(t, svc, minCfg("/usr/bin/true"), app.LoopOptions{Worker: "w"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, "done", r.Status)
		require.Empty(t, r.Error)
	}
	for _, id := range ids {
		got, showErr := svc.Show(ctx, id)
		require.NoError(t, showErr)
		require.Equal(t, task.StatusDone, got.Status)
	}
}

// 2. Failure path: /bin/false → task marked failed, Status="failed".
func TestRunLoopFailure(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ctx := context.Background()
	id := addReady(t, svc, 1)[0]

	results, err := runLoop(t, svc, minCfg("/usr/bin/false"), app.LoopOptions{Worker: "w"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "failed", results[0].Status)
	require.NotEmpty(t, results[0].Error)

	got, showErr := svc.Show(ctx, id)
	require.NoError(t, showErr)
	require.Equal(t, task.StatusFailed, got.Status)
}

// 3. Timeout path: command sleeps longer than TaskTimeout → ErrAgentTimeout,
// Status="timeout", task failed.
func TestRunLoopTimeout(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ctx := context.Background()
	id := addReady(t, svc, 1)[0]

	cfg := minCfg("/bin/sleep 30")
	cfg.TaskTimeout = 50 * time.Millisecond

	results, err := runLoop(t, svc, cfg, app.LoopOptions{Worker: "w"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "timeout", results[0].Status)
	require.NotEmpty(t, results[0].Error)

	got, showErr := svc.Show(ctx, id)
	require.NoError(t, showErr)
	require.Equal(t, task.StatusFailed, got.Status)
}

// 4. Circuit breaker: MaxConsecutiveFailures=2, all-failing command, >2 tasks →
// RunLoop stops after 2 consecutive failures.
func TestRunLoopCircuitBreaker(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	addReady(t, svc, 5)

	cfg := minCfg("/usr/bin/false")
	cfg.MaxConsecutiveFailures = 2

	results, err := runLoop(t, svc, cfg, app.LoopOptions{Worker: "w"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "circuit breaker")
	require.Len(t, results, 2, "breaker trips after exactly 2 consecutive failures")
}

// 5. Empty queue: no ready tasks → RunLoop returns nil cleanly, emit never called.
func TestRunLoopEmptyQueue(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	var emitCalled int
	err := svc.RunLoop(
		context.Background(), minCfg("/usr/bin/true"),
		app.LoopOptions{Worker: "w"},
		nil, nil,
		func(app.LoopResult) error { emitCalled++; return nil },
	)
	require.NoError(t, err)
	require.Equal(t, 0, emitCalled)
}

// 6. MaxTasks: seed 5, opts.MaxTasks=2 → exactly 2 processed.
func TestRunLoopMaxTasks(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	addReady(t, svc, 5)

	results, err := runLoop(t, svc, minCfg("/usr/bin/true"), app.LoopOptions{Worker: "w", MaxTasks: 2})
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, "done", r.Status)
	}
}

// 7. Context cancellation: cancel ctx → RunLoop returns promptly with ctx error.
func TestRunLoopContextCancellation(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	addReady(t, svc, 1)

	cfg := minCfg("/bin/sleep 30")
	cfg.TaskTimeout = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		var results []app.LoopResult
		loopErr := svc.RunLoop(ctx, cfg, app.LoopOptions{Worker: "w"}, nil, nil,
			func(r app.LoopResult) error { results = append(results, r); return nil },
		)
		errCh <- loopErr
	}()
	cancel()

	select {
	case loopErr := <-errCh:
		require.ErrorIs(t, loopErr, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("RunLoop did not return after context cancellation")
	}
}

// 7b. Context already cancelled before first iteration — emit never called.
func TestRunLoopContextPreCancelled(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	addReady(t, svc, 2)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var emitCalled int
	err := svc.RunLoop(ctx, minCfg("/usr/bin/true"), app.LoopOptions{Worker: "w"},
		nil, nil,
		func(app.LoopResult) error { emitCalled++; return nil },
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, emitCalled)
}

// 8. HeartbeatInterval exercised: RunLoop completes correctly with a non-zero
// heartbeat so the goroutine code path is hit.
func TestRunLoopWithHeartbeatInterval(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ctx := context.Background()
	id := addReady(t, svc, 1)[0]

	cfg := minCfg("/usr/bin/true")
	cfg.HeartbeatInterval = 10 * time.Millisecond

	results, err := runLoop(t, svc, cfg, app.LoopOptions{Worker: "w"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "done", results[0].Status)

	got, showErr := svc.Show(ctx, id)
	require.NoError(t, showErr)
	require.Equal(t, task.StatusDone, got.Status)
}

// emit error stops the loop.
func TestRunLoopEmitErrorStopsLoop(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	addReady(t, svc, 3)

	boom := errors.New("sink full")
	var called int
	err := svc.RunLoop(
		context.Background(), minCfg("/usr/bin/true"),
		app.LoopOptions{Worker: "w"},
		nil, nil,
		func(app.LoopResult) error { called++; return boom },
	)
	require.ErrorIs(t, err, boom)
	require.Equal(t, 1, called)
}
