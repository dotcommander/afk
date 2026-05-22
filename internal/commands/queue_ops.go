package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

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
		Use:   "take",
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
				ready, err := d.Service.Ready(cmd.Context())
				if err != nil {
					return err
				}
				readyCount := len(ready)
				if limit > 0 && len(ready) > limit {
					ready = ready[:limit]
				}
				bodyLimit := output.DefaultListBodyRunes
				if full {
					bodyLimit = 0
				}
				bodyHint := ""
				if !full {
					bodyHint = "use --full to see the complete task body"
				}
				if envelope || summary {
					snapshot, err := d.Service.Status(cmd.Context())
					if err != nil {
						return err
					}
					return output.WriteTakePreview(d.Stdout, ready, snapshot.Counts, readyCount, bodyLimit, bodyHint)
				}
				return output.WriteListWithBodyLimitHint(d.Stdout, ready, asJSON, bodyLimit, bodyHint)
			}
			claimed, err := d.Service.Take(cmd.Context(), leaseDuration, workerID, "")
			if err != nil {
				return err
			}
			if claimed == nil {
				return writeNoReadyExplanation(cmd, d)
			}
			if err := task.ValidateAddOptions(task.AddOptionsFromTask(*claimed)); err != nil {
				if failErr := d.Service.Fail(cmd.Context(), claimed.ID, err.Error()); failErr != nil {
					return failErr
				}
				return fmt.Errorf("pop %s: %w", claimed.ID, err)
			}
			if summary || envelope {
				return writeTakeSummary(cmd, d, *claimed, full)
			}
			if full {
				return output.WriteBoundTaskJSONLine(d.Stdout, *claimed, 0, "pop")
			}
			return output.WriteTaskJSONLine(d.Stdout, *claimed, "pop")
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
	todo, err := d.Service.List(cmd.Context(), string(task.StatusPending))
	if err != nil {
		return err
	}
	if len(todo) == 0 {
		_, err = fmt.Fprintln(d.Stderr, "No ready tasks: queue has no todo tasks.")
		return err
	}

	doing, err := d.Service.List(cmd.Context(), string(task.StatusWorking))
	if err != nil {
		return err
	}
	activeResources := make(map[string]struct{}, len(doing))
	for _, t := range doing {
		if t.ResourceKey != "" {
			activeResources[t.ResourceKey] = struct{}{}
		}
	}

	resourceBlocked := 0
	for _, t := range todo {
		if _, ok := activeResources[t.ResourceKey]; t.ResourceKey != "" && ok {
			resourceBlocked++
		}
	}

	if resourceBlocked > 0 {
		_, err = fmt.Fprintf(d.Stderr, "No ready tasks: %d todo task(s) blocked by active resource locks; %d todo task(s) total.\n", resourceBlocked, len(todo))
		return err
	}
	_, err = fmt.Fprintf(d.Stderr, "No ready tasks: %d todo task(s) blocked by dependencies.\n", len(todo))
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
			dur, err := time.ParseDuration(olderThan)
			if err != nil {
				return fmt.Errorf("parse older-than: %w", err)
			}
			tasks, err := d.Service.RequeueStale(cmd.Context(), dur)
			if err != nil {
				return err
			}
			for _, t := range tasks {
				if _, err := fmt.Fprintln(d.Stdout, t.ID); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "1h", "requeue doing tasks older than this duration when no lease is set")
	return cmd
}

func parseOptionalDuration(name, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
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
