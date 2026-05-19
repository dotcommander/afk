// Package runner executes claimed queue tasks with an explicit command template.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

const defaultHeartbeatMinInterval = time.Second

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
	if opts.Workers == 0 {
		opts.Workers = 1
	}
	if opts.Workers != 1 {
		return fmt.Errorf("runner: --workers > 1 is not implemented yet")
	}
	if opts.Limit < 0 {
		return fmt.Errorf("runner: --limit must be non-negative")
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.DryRun {
		return writeDryRun(ctx, opts.Stdout, service, opts)
	}
	if strings.TrimSpace(opts.ExecTemplate) == "" {
		return fmt.Errorf("runner: --exec is required unless --dry-run is set")
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

func writeDryRun(ctx context.Context, w io.Writer, service *app.Service, opts Options) error {
	ready, err := service.Ready(ctx)
	if err != nil {
		return err
	}
	if opts.Limit > 0 && len(ready) > opts.Limit {
		ready = ready[:opts.Limit]
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WOULD_RUN\tCOMMAND") //nolint:errcheck // tabwriter buffers; errors surface at Flush
	if len(ready) == 0 {
		fmt.Fprintln(tw, "none\t") //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	for _, t := range ready {
		command := "(set --exec to preview command)"
		if opts.ExecTemplate != "" {
			command = renderCommand(opts.ExecTemplate, t, opts.QueuePath)
		}
		fmt.Fprintf(tw, "%s\t%s\n", t.ID, command) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("runner: write dry-run: %w", err)
	}

	pending, err := service.List(ctx, string(task.StatusPending))
	if err != nil {
		return err
	}
	waiting := false
	for _, t := range pending {
		info, err := service.Why(ctx, t.ID)
		if err != nil {
			return err
		}
		if info.Ready {
			continue
		}
		if !waiting {
			if _, err := fmt.Fprintln(w, "WAITING\tREASON"); err != nil {
				return fmt.Errorf("runner: write waiting header: %w", err)
			}
			waiting = true
		}
		for _, reason := range info.Reasons {
			if _, err := fmt.Fprintf(w, "%s\t%s: %s\n", t.ID, reason.Kind, reason.Detail); err != nil {
				return fmt.Errorf("runner: write waiting: %w", err)
			}
		}
	}
	return nil
}

func runTask(ctx context.Context, service *app.Service, t task.Task, opts Options) error {
	commandText := renderCommand(opts.ExecTemplate, t, opts.QueuePath)
	if _, err := fmt.Fprintf(opts.Stdout, "running %s\n", t.ID); err != nil {
		return fmt.Errorf("runner: write start: %w", err)
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", commandText)
	if t.CWD != "" {
		cmd.Dir = t.CWD
	}
	if opts.QueuePath != "" {
		cmd.Env = append(os.Environ(), "AFK_QUEUE="+opts.QueuePath)
	}
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

	current, showErr := service.Show(ctx, t.ID)
	if showErr != nil {
		return showErr
	}
	if current.Status == task.StatusWorking {
		reason := "runner command exited without finalizing task"
		if runErr != nil {
			reason = "runner command failed: " + runErr.Error()
		}
		if err := service.Fail(ctx, t.ID, reason); err != nil {
			return err
		}
	}
	if runErr != nil {
		return fmt.Errorf("runner: command for %s: %w", t.ID, runErr)
	}
	return nil
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
					errs <- fmt.Errorf("runner: heartbeat %s: %w", taskID, err)
					return
				}
			}
		}
	}()
}

func firstHeartbeatErr(errs <-chan error) error {
	for err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

func renderCommand(template string, t task.Task, queuePath string) string {
	replacer := strings.NewReplacer(
		"{{id}}", t.ID,
		"{{cwd}}", t.CWD,
		"{{body}}", t.Body,
		"{{queue}}", queuePath,
	)
	return replacer.Replace(template)
}
