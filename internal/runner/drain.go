package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

// drainPoisonThreshold is the failed-attempt count at which a task is
// considered poison: a task that has failed this many times is blocked so the
// drain loop stops re-claiming it instead of looping on the same failure.
const drainPoisonThreshold = 3

// Drain claims ready tasks and executes the configured command template in a
// bounded loop, exactly like Run, but with a poison guard: before each claim
// it blocks any ready task that has already failed drainPoisonThreshold times
// so a repeatedly failing task cannot stall the drain.
//
// Drain shares Run's executor: the binary cannot execute task bodies itself,
// so an --exec template (an agent or shell worker) is still required. The only
// difference from Run is the pre-claim poison sweep.
func Drain(ctx context.Context, service *app.Service, opts Options) error {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.DryRun {
		return writeDryRun(ctx, opts.Stdout, service, opts)
	}
	if err := validateRunOptions(opts); err != nil {
		return err
	}
	if opts.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.MaxDuration)
		defer cancel()
	}

	processed := 0
	for opts.Limit == 0 || processed < opts.Limit {
		if _, err := blockPoisonTasks(ctx, service, opts.Stdout); err != nil {
			return err
		}
		claimed, err := service.PopWithLeaseForWorker(ctx, opts.Lease, opts.WorkerID, "")
		if err != nil {
			return err
		}
		if claimed == nil {
			return nil
		}
		if err := runTask(ctx, service, *claimed, opts); err != nil {
			return err
		}
		processed++
	}
	return nil
}

// blockPoisonTasks blocks every ready task that has failed at least
// drainPoisonThreshold times, returning the count blocked. A poisoned task is
// reported to w so the operator sees why it was skipped.
func blockPoisonTasks(ctx context.Context, service *app.Service, w io.Writer) (int, error) {
	ready, err := service.Ready(ctx)
	if err != nil {
		return 0, err
	}
	blocked := 0
	for _, t := range ready {
		fails, err := failedAttemptCount(ctx, service, t.ID)
		if err != nil {
			return blocked, err
		}
		if fails < drainPoisonThreshold {
			continue
		}
		reason := fmt.Sprintf("poison: %d failed attempts", fails)
		if err := service.Block(ctx, t.ID, reason); err != nil {
			return blocked, err
		}
		if _, err := fmt.Fprintf(w, "blocked %s (%s)\n", t.ID, reason); err != nil {
			return blocked, fmt.Errorf("drain: write: %w", err)
		}
		blocked++
	}
	return blocked, nil
}

func failedAttemptCount(ctx context.Context, service *app.Service, id string) (int, error) {
	data, err := service.Explain(ctx, id)
	if err != nil {
		return 0, err
	}
	fails := 0
	for _, attempt := range data.Attempts {
		if attempt.Status == task.StatusFailed {
			fails++
		}
	}
	return fails, nil
}
