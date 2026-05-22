package runner

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

// This file renders dry-run output for the legacy internal runner: the ready
// tasks that would be claimed and run, plus todo tasks not currently claimable.
// It mutates nothing. The public CLI uses `afk take --dry-run` instead.

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
	todo, err := service.List(ctx, string(task.StatusPending))
	if err != nil {
		return err
	}
	ready, err := service.Ready(ctx)
	if err != nil {
		return err
	}
	readyIDs := make(map[string]struct{}, len(ready))
	for _, t := range ready {
		readyIDs[t.ID] = struct{}{}
	}
	waiting := false
	for _, t := range todo {
		if _, ok := readyIDs[t.ID]; ok {
			continue
		}
		if !waiting {
			if _, err := fmt.Fprintln(w, "WAITING\tSTATUS"); err != nil {
				return fmt.Errorf("runner: write waiting header: %w", err)
			}
			waiting = true
		}
		if _, err := fmt.Fprintf(w, "%s\tnot ready\n", t.ID); err != nil {
			return fmt.Errorf("runner: write waiting: %w", err)
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
