package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

func newPruneCmd(d *Deps) *cobra.Command {
	var statusCSV string
	var tag string

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove tasks by status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tag != "" && cmd.Flags().Changed("status") {
				return fmt.Errorf("prune: --tag and --status are mutually exclusive")
			}
			if tag != "" {
				n, err := d.Service.PruneByTag(cmd.Context(), tag)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(d.Stdout, "pruned %d tasks (tag=%s)\n", n, tag)
				return err
			}
			var statuses []string
			for _, s := range strings.Split(statusCSV, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					statuses = append(statuses, s)
				}
			}
			parsed := make([]task.Status, 0, len(statuses))
			for _, status := range statuses {
				parsed = append(parsed, task.Status(status))
			}
			return d.Service.Prune(cmd.Context(), parsed)
		},
	}
	cmd.Flags().StringVar(&statusCSV, "status", "done,failed", "comma-separated statuses to prune")
	cmd.Flags().StringVar(&tag, "tag", "", "delete all tasks with this tag")
	return cmd
}

func newPopCmd(d *Deps) *cobra.Command {
	var lease string
	var workerID string

	cmd := &cobra.Command{
		Use:   "pop",
		Short: "Claim the first pending task (sets status to working)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var leaseDuration time.Duration
			if lease != "" {
				var err error
				leaseDuration, err = time.ParseDuration(lease)
				if err != nil {
					return fmt.Errorf("parse lease: %w", err)
				}
			}
			claimed, err := d.Service.PopWithLeaseForWorker(cmd.Context(), leaseDuration, workerID, "")
			if err != nil {
				return err
			}
			if claimed == nil {
				return nil
			}
			return output.WriteJSONLine(d.Stdout, claimed, "pop")
		},
	}
	cmd.Flags().StringVar(&lease, "lease", "", "lease duration for the claim (for example 30m)")
	cmd.Flags().StringVar(&workerID, "worker", "", "worker id for the claim")
	return cmd
}

func newRetryCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "retry <id>",
		Short: "Reset a failed task to pending",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Retry(cmd.Context(), args[0])
		},
	}
}

func newRequeueStaleCmd(d *Deps) *cobra.Command {
	var olderThan string

	cmd := &cobra.Command{
		Use:   "requeue-stale",
		Short: "Reset stale working tasks to pending",
		RunE: func(cmd *cobra.Command, _ []string) error {
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
	cmd.Flags().StringVar(&olderThan, "older-than", "1h", "requeue working tasks older than this duration when no lease is set")
	return cmd
}

func newHeartbeatCmd(d *Deps) *cobra.Command {
	var workerID string
	var lease string

	cmd := &cobra.Command{
		Use:   "heartbeat <id>",
		Short: "Extend a worker-owned task lease",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			leaseDuration, err := time.ParseDuration(lease)
			if err != nil {
				return fmt.Errorf("parse lease: %w", err)
			}
			return d.Service.Heartbeat(cmd.Context(), args[0], workerID, leaseDuration)
		},
	}
	cmd.Flags().StringVar(&workerID, "worker", "", "worker id that owns the claim")
	cmd.Flags().StringVar(&lease, "lease", "30m", "new lease duration from now")
	_ = cmd.MarkFlagRequired("worker")
	return cmd
}
