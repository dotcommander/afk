package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

// takeKind is the command name and the JSON "kind" token emitted for claimed tasks.
const takeKind = "take"

// reapKind is the command name for the cron-driven stale-task reaper.
const reapKind = "reap"

func newTakeCmd(d *Deps) *cobra.Command {
	var lease string
	var workerID string
	var dryRun bool
	var limit int
	var asJSON bool
	var summary bool
	var full bool
	var envelope bool

	cmd := &cobra.Command{
		Use:   takeKind,
		Short: "Claim the first ready task",
		Long: strings.TrimSpace(`Claim the first ready task.

Agent loop:
  afk take --worker <name> --lease 60m --summary
  # execute exactly one returned task
  afk set <id> done --note "<verification>"
  # or
  afk set <id> failed --note "<one-line reason>"

Use --dry-run to preview ready tasks without claiming. If preview output shows
body_truncated=true, add --full to inspect complete task bodies.`),
		RunE: func(cmd *cobra.Command, _ []string) error {
			leaseDuration, err := parseOptionalDuration("lease", lease)
			if err != nil {
				return err
			}
			if dryRun {
				return runTakeDryRun(cmd, d, takeDryRunOptions{limit: limit, asJSON: asJSON, summary: summary, full: full, envelope: envelope})
			}
			return runTakeClaim(cmd, d, leaseDuration, workerID, summary, full, envelope)
		},
	}
	cmd.Flags().StringVar(&lease, "lease", "", "lease duration for the claim (for example 30m)")
	cmd.Flags().StringVar(&workerID, "worker", "", "worker id for the claim")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview ready tasks without claiming")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum ready tasks to print with --dry-run")
	cmd.Flags().BoolVar(&asJSON, "json", true, "emit JSONL output; enabled by default")
	cmd.Flags().BoolVar(&summary, "summary", false, "include queue counts with the claimed task or dry-run preview")
	cmd.Flags().BoolVar(&full, "full", false, "include full task bodies in JSON output")
	cmd.Flags().BoolVar(&envelope, "envelope", false, "emit a stable JSON object envelope")
	return cmd
}

type takeDryRunOptions struct {
	limit    int
	asJSON   bool
	summary  bool
	full     bool
	envelope bool
}

func runTakeDryRun(cmd *cobra.Command, d *Deps, opts takeDryRunOptions) error {
	ready, err := d.Service.Ready(cmd.Context())
	if err != nil {
		return err
	}
	readyCount := len(ready)
	if opts.limit > 0 && len(ready) > opts.limit {
		ready = ready[:opts.limit]
	}
	bodyLimit, bodyHint := takePreviewBodyPolicy(opts.full)
	if opts.envelope || opts.summary {
		snapshot, err := d.Service.Status(cmd.Context())
		if err != nil {
			return err
		}
		return output.WriteTakePreview(d.Stdout, ready, snapshot.Counts, readyCount, bodyLimit, bodyHint)
	}
	return output.WriteListWithBodyLimitHint(d.Stdout, ready, opts.asJSON, bodyLimit, bodyHint)
}

func takePreviewBodyPolicy(full bool) (int, string) {
	if full {
		return 0, ""
	}
	return output.DefaultListBodyRunes, "use --full to see the complete task body"
}

func runTakeClaim(cmd *cobra.Command, d *Deps, leaseDuration time.Duration, workerID string, summary, full, envelope bool) error {
	claimed, err := d.Service.Take(cmd.Context(), leaseDuration, workerID, "")
	if err != nil {
		return err
	}
	if claimed == nil {
		return writeNoReadyExplanation(cmd, d)
	}
	if err := task.ValidateAddOptions(task.AddOptionsFromTask(*claimed)); err != nil {
		if failErr := d.Service.Fail(cmd.Context(), claimed.ID, err.Error()); failErr != nil {
			return fmt.Errorf("take %s: invalid claimed task %v; auto-fail: %w", claimed.ID, err, failErr)
		}
		return fmt.Errorf("take %s: %w", claimed.ID, err)
	}
	if summary || envelope {
		return writeTakeSummary(cmd, d, *claimed, full)
	}
	if full {
		return output.WriteBoundTaskJSONLine(d.Stdout, *claimed, 0, takeKind)
	}
	return output.WriteTaskJSONLine(d.Stdout, *claimed, takeKind)
}

func writeTakeSummary(cmd *cobra.Command, d *Deps, claimed task.Task, full bool) error {
	snapshot, err := d.Service.Status(cmd.Context())
	if err != nil {
		return err
	}
	ready, err := d.Service.Ready(cmd.Context())
	if err != nil {
		return err
	}
	if full {
		return output.WriteTakeSummaryFull(d.Stdout, claimed, snapshot.Counts, len(ready))
	}
	return output.WriteTakeSummary(d.Stdout, claimed, snapshot.Counts, len(ready))
}

func writeNoReadyExplanation(cmd *cobra.Command, d *Deps) error {
	e, err := d.Service.ExplainNotReady(cmd.Context())
	if err != nil {
		return err
	}
	if e.TodoTotal == 0 {
		_, err = fmt.Fprintln(d.Stderr, "No ready tasks: queue has no todo tasks.")
		return err
	}
	_, err = fmt.Fprintf(d.Stderr, "No ready tasks: %d of %d todo task(s) blocked by dependencies, resource locks, or unsatisfied gates.\n", e.Blocked, e.TodoTotal)
	return err
}

func newRequeueStaleCmd(d *Deps) *cobra.Command {
	var olderThan string

	cmd := &cobra.Command{
		Use:    "requeue-stale",
		Short:  "Reset stale doing tasks to todo",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := warnDeprecated(d.Stderr, "afk requeue-stale", "afk task <id>, afk set <id> failed --note <reason>, then afk add <replacement task>"); err != nil {
				return err
			}
			dur, err := parseRequiredDuration("older-than", olderThan)
			if err != nil {
				return err
			}
			return runRequeueStale(cmd, d, dur)
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "1h", "requeue doing tasks older than this duration when no lease is set")
	return cmd
}

func newReapCmd(d *Deps) *cobra.Command {
	var olderThan string

	cmd := &cobra.Command{
		Use:   reapKind,
		Short: "Reset stale doing tasks to todo (cron-driven reaper)",
		Long: strings.TrimSpace(`Reset stale doing tasks back to todo so they become claimable again.

A task is stale when its lease has expired, or (when it has no lease) when its
start time is older than --older-than. Intended to run unattended on a fixed
interval, for example a cron entry:

  */10 * * * * afk reap --older-than 20m

Prints the id of each requeued task to stdout, one per line.`),
		RunE: func(cmd *cobra.Command, _ []string) error {
			dur, err := parseRequiredDuration("older-than", olderThan)
			if err != nil {
				return err
			}
			return runRequeueStale(cmd, d, dur)
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "20m", "requeue doing tasks older than this duration when no lease is set")
	return cmd
}

func runRequeueStale(cmd *cobra.Command, d *Deps, olderThan time.Duration) error {
	tasks, err := d.Service.RequeueStale(cmd.Context(), olderThan)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if _, err := fmt.Fprintln(d.Stdout, t.ID); err != nil {
			return err
		}
	}
	return nil
}

func parseOptionalDuration(name, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	return parseRequiredDuration(name, value)
}

func parseRequiredDuration(name, value string) (time.Duration, error) {
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if dur <= 0 {
		return 0, fmt.Errorf("parse %s: duration must be positive", name)
	}
	return dur, nil
}

func newHeartbeatCmd(d *Deps) *cobra.Command {
	var workerID string
	var lease string

	cmd := &cobra.Command{
		Use:    "heartbeat <id>",
		Short:  "Extend a worker-owned task lease",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := warnDeprecated(d.Stderr, "afk heartbeat", "afk take --lease <duration> with a long enough lease"); err != nil {
				return err
			}
			leaseDuration, err := parseOptionalDuration("lease", lease)
			if err != nil {
				return err
			}
			return d.Service.Heartbeat(cmd.Context(), args[0], workerID, leaseDuration)
		},
	}
	cmd.Flags().StringVar(&workerID, "worker", "", "worker id that owns the claim")
	cmd.Flags().StringVar(&lease, "lease", "30m", "new lease duration from now")
	_ = cmd.MarkFlagRequired("worker")
	return cmd
}
