package commands

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/output"
)

func newLoopCmd(d *Deps) *cobra.Command {
	var (
		command     string
		worker      string
		lease       time.Duration
		timeout     time.Duration
		cooldown    time.Duration
		heartbeat   time.Duration
		maxFailures int
		maxTasks    int
	)

	cmd := &cobra.Command{
		Use:   "loop",
		Short: "Autonomous worker-driver: claim tasks and run an agent command per task",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := app.LoadLoopConfig()

			if cmd.Flags().Changed("command") {
				cfg.Command = command
			}
			if cmd.Flags().Changed("worker") {
				cfg.Worker = worker
			}
			if cmd.Flags().Changed("lease") {
				cfg.Lease = lease
			}
			if cmd.Flags().Changed("timeout") {
				cfg.TaskTimeout = timeout
			}
			if cmd.Flags().Changed("cooldown") {
				cfg.Cooldown = cooldown
			}
			if cmd.Flags().Changed("heartbeat") {
				cfg.HeartbeatInterval = heartbeat
			}
			if cmd.Flags().Changed("max-failures") {
				cfg.MaxConsecutiveFailures = maxFailures
			}

			// Derive worker identity when neither flag nor config provides one.
			if cfg.Worker == "" {
				cfg.Worker = fmt.Sprintf("loop-%d", os.Getpid())
			}

			// Fail closed: no command means nothing to run.
			if cfg.Command == "" {
				return fmt.Errorf("no agent command configured (set 'command' in ~/.config/afk/loop.yaml or pass --command)")
			}

			opts := app.LoopOptions{
				Worker: cfg.Worker,
			}
			if cmd.Flags().Changed("max-tasks") {
				opts.MaxTasks = maxTasks
			}

			// Agent output goes to Stderr so stdout remains pure JSONL.
			return d.Service.RunLoop(
				cmd.Context(),
				cfg,
				opts,
				d.Stderr,
				d.Stderr,
				func(r app.LoopResult) error {
					return output.WriteJSONLine(d.Stdout, r, "loop")
				},
			)
		},
	}

	cmd.Flags().StringVar(&command, "command", "", "agent command template (e.g. \"claude -p {{.Prompt}}\")")
	cmd.Flags().StringVar(&worker, "worker", "", "worker identity (default: loop-<pid>)")
	cmd.Flags().DurationVar(&lease, "lease", 0, "exclusive claim duration taken on each task")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "per-task execution timeout")
	cmd.Flags().DurationVar(&cooldown, "cooldown", 0, "pause between loop ticks when no task is found")
	cmd.Flags().DurationVar(&heartbeat, "heartbeat", 0, "interval for extending the task lease while running")
	cmd.Flags().IntVar(&maxFailures, "max-failures", 0, "halt loop after this many consecutive task failures")
	cmd.Flags().IntVar(&maxTasks, "max-tasks", 0, "exit cleanly after processing this many tasks (0 = unlimited)")

	return cmd
}
