package commands

import (
	"fmt"
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

	cmd := &cobra.Command{
		Use:   "take",
		Short: "Claim the first ready task",
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
				if limit >= 0 && len(ready) > limit {
					ready = ready[:limit]
				}
				return output.WriteList(d.Stdout, ready, asJSON)
			}
			claimed, err := d.Service.Take(cmd.Context(), leaseDuration, workerID, "")
			if err != nil {
				return err
			}
			if claimed == nil {
				return nil
			}
			if err := task.ValidateAddOptions(task.AddOptionsFromTask(*claimed)); err != nil {
				if failErr := d.Service.Fail(cmd.Context(), claimed.ID, err.Error()); failErr != nil {
					return failErr
				}
				return fmt.Errorf("pop %s: %w", claimed.ID, err)
			}
			return output.WriteTaskJSONLine(d.Stdout, *claimed, "pop")
		},
	}
	cmd.Flags().StringVar(&lease, "lease", "", "lease duration for the claim (for example 30m)")
	cmd.Flags().StringVar(&workerID, "worker", "", "worker id for the claim")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview ready tasks without claiming")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum ready tasks to print with --dry-run")
	cmd.Flags().BoolVar(&asJSON, "json", true, "emit JSONL output")
	return cmd
}

func newRequeueStaleCmd(d *Deps) *cobra.Command {
	var olderThan string

	cmd := &cobra.Command{
		Use:    "requeue-stale",
		Short:  "Reset stale doing tasks to todo",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := warnDeprecated(d.Stderr, "afk requeue-stale", "afk task <id>, afk set <id> failed <reason>, then afk add <replacement task>"); err != nil {
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
