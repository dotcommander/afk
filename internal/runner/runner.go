// Package runner executes claimed queue tasks with an explicit command template.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

const (
	defaultHeartbeatMinInterval = time.Second
	cleanupTimeout              = 5 * time.Second
)

// Options controls one runner invocation.
type Options struct {
	DryRun       bool
	Limit        int
	MaxDuration  time.Duration
	Lease        time.Duration
	WorkerID     string
	ExecTemplate string
	QueuePath    string
	Workers      int
	Stdout       io.Writer
	Stderr       io.Writer
}

// Run claims ready tasks and executes the configured command template.
func Run(ctx context.Context, service *app.Service, opts Options) error {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.DryRun {
		if err := validateRunnerShapeOptions(opts); err != nil {
			return err
		}
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

// validateRunOptions enforces the shared option contract for the non-dry-run
// claim loop. Both Run and Drain gate on it before claiming any task.
func validateRunOptions(opts Options) error {
	if err := validateRunnerShapeOptions(opts); err != nil {
		return err
	}
	if strings.TrimSpace(opts.ExecTemplate) == "" {
		return fmt.Errorf("runner: --exec is required unless --dry-run is set")
	}
	return nil
}

func validateRunnerShapeOptions(opts Options) error {
	if opts.Workers != 0 && opts.Workers != 1 {
		return fmt.Errorf("runner: --workers > 1 is not implemented yet")
	}
	if opts.Limit < 0 {
		return fmt.Errorf("runner: --limit must be non-negative")
	}
	return nil
}

func runTask(ctx context.Context, service *app.Service, t task.Task, opts Options) error {
	if err := task.ValidateAddOptions(task.AddOptionsFromTask(t)); err != nil {
		return failInvalidTask(ctx, service, t.ID, err)
	}
	commandText := renderCommand(opts.ExecTemplate, t, opts.QueuePath)
	if _, err := fmt.Fprintf(opts.Stdout, "running %s\n", t.ID); err != nil {
		return fmt.Errorf("runner: write start: %w", err)
	}

	// newShellCommand is platform-specific: /bin/sh on Unix, cmd.exe on Windows.
	// It also wires cmd.Cancel to kill the whole process group/tree so that
	// children spawned by the shell (afk done, agent CLIs) are not orphaned.
	cmd := newShellCommand(ctx, commandText)
	if t.CWD != "" {
		cmd.Dir = t.CWD
	}
	env := os.Environ()
	if opts.QueuePath != "" {
		env = append(env, "AFK_QUEUE="+opts.QueuePath)
	}
	env = append(env,
		"AFK_TASK_ID="+t.ID,
		"AFK_TASK_BODY="+t.Body,
		"AFK_TASK_CWD="+t.CWD,
	)
	cmd.Env = env
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	hbDone := make(chan struct{})
	hbErr := make(chan error, 1)
	startHeartbeat(ctx, service, t.ID, opts, hbDone, hbErr)

	runErr := cmd.Run()
	close(hbDone)
	if err := firstHeartbeatErr(hbErr); err != nil {
		return err
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancelCleanup()
	current, showErr := service.Show(cleanupCtx, t.ID)
	if showErr != nil {
		return showErr
	}
	if current.Status == task.StatusWorking {
		reason := "runner command exited without finalizing task"
		if runErr != nil {
			reason = "runner command failed: " + runErr.Error()
		}
		if err := service.Fail(cleanupCtx, t.ID, reason); err != nil {
			return err
		}
	}
	if runErr != nil {
		return fmt.Errorf("runner: command for %s: %w", t.ID, runErr)
	}
	return nil
}

func failInvalidTask(ctx context.Context, service *app.Service, taskID string, err error) error {
	if failErr := service.Fail(ctx, taskID, err.Error()); failErr != nil {
		return failErr
	}
	return fmt.Errorf("runner: %s: %w", taskID, err)
}

func startHeartbeat(ctx context.Context, service *app.Service, taskID string, opts Options, done <-chan struct{}, errs chan<- error) {
	if opts.Lease <= 0 {
		close(errs)
		return
	}
	interval := opts.Lease / 2
	if interval < defaultHeartbeatMinInterval {
		interval = defaultHeartbeatMinInterval
	}
	go func() {
		defer close(errs)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := service.Heartbeat(ctx, taskID, opts.WorkerID, opts.Lease); err != nil {
					if taskNoLongerWorking(ctx, service, taskID) {
						return
					}
					errs <- fmt.Errorf("runner: heartbeat %s: %w", taskID, err)
					return
				}
			}
		}
	}()
}

func taskNoLongerWorking(ctx context.Context, service *app.Service, taskID string) bool {
	current, err := service.Show(context.WithoutCancel(ctx), taskID)
	return err == nil && current.Status != task.StatusWorking
}

func firstHeartbeatErr(errs <-chan error) error {
	for err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}
	return nil
}

func renderCommand(template string, t task.Task, queuePath string) string {
	replacer := strings.NewReplacer(
		"{{id}}", shellQuote(t.ID),
		"{{cwd}}", shellQuote(t.CWD),
		"{{body}}", shellQuote(t.Body),
		"{{queue}}", shellQuote(queuePath),
	)
	return replacer.Replace(template)
}

// shellQuote wraps s in POSIX single quotes so the shell treats it as one
// literal word. Embedded single quotes are closed, escaped, and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
