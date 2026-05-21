package runner

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

// This file renders `--dry-run` output for the runner: the tasks that would be
// claimed and run, plus the pending tasks that are waiting and why. It mutates
// nothing. The live claim/exec loop lives in runner.go.

const maxDryRunCommandRunes = 1000

func writeDryRun(ctx context.Context, w io.Writer, service *app.Service, opts Options) error {
	if err := writeRunnableDryRun(ctx, w, service, opts); err != nil {
		return err
	}
	return writeWaitingDryRun(ctx, w, service)
}

func writeRunnableDryRun(ctx context.Context, w io.Writer, service *app.Service, opts Options) error {
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
		command := dryRunCommand(t, opts)
		fmt.Fprintf(tw, "%s\t%s\n", t.ID, command) //nolint:errcheck // tabwriter buffers; errors surface at Flush
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("runner: write dry-run: %w", err)
	}
	return nil
}

func writeWaitingDryRun(ctx context.Context, w io.Writer, service *app.Service) error {
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

func dryRunCommand(t task.Task, opts Options) string {
	if opts.ExecTemplate == "" {
		return "(set --exec to preview command)"
	}
	return truncateRunes(renderCommand(opts.ExecTemplate, t, opts.QueuePath), maxDryRunCommandRunes)
}
