package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/runner"
)

func newRunCmd(d *Deps) *cobra.Command {
	var opts runner.Options
	var lease string
	var maxMinutes int

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Claim ready tasks and execute a worker command",
		RunE: func(cmd *cobra.Command, _ []string) error {
			leaseDuration, err := time.ParseDuration(lease)
			if err != nil {
				return fmt.Errorf("parse lease: %w", err)
			}
			opts.Lease = leaseDuration
			if maxMinutes > 0 {
				opts.MaxDuration = time.Duration(maxMinutes) * time.Minute
			}
			opts.QueuePath = d.QueuePaths.SQLitePath
			opts.Stdout = d.Stdout
			opts.Stderr = d.Stderr
			return runner.Run(cmd.Context(), d.Service, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show runnable and waiting tasks without claiming")
	cmd.Flags().IntVar(&opts.Limit, "limit", 1, "maximum tasks to process; 0 means no limit")
	cmd.Flags().IntVar(&maxMinutes, "max-minutes", 0, "maximum runner runtime in minutes")
	cmd.Flags().StringVar(&lease, "lease", "30m", "lease duration for each claim")
	cmd.Flags().StringVar(&opts.WorkerID, "worker", "", "worker id for claims and heartbeats")
	cmd.Flags().StringVar(&opts.ExecTemplate, "exec", "", "shell command template using {{id}}, {{cwd}}, and {{body}}")
	cmd.Flags().IntVar(&opts.Workers, "workers", 1, "number of parallel workers; only 1 is supported in this version")
	return cmd
}
