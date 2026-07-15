package commands

import (
	"context"
	"fmt"
	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/output"
	"os"
	"time"
)

type LoopCmd struct {
	Command     *string        `help:"Agent command template (e.g. 'claude -p {{.Prompt}}')."`
	Worker      *string        `help:"Worker identity (default: loop-<pid>)."`
	Lease       *time.Duration `help:"Exclusive claim duration taken on each task."`
	Timeout     *time.Duration `help:"Per-task execution timeout."`
	Cooldown    *time.Duration `help:"Pause between loop ticks when no task is found."`
	Heartbeat   *time.Duration `help:"Interval for extending the task lease while running."`
	MaxFailures *int           `name:"max-failures" help:"Halt loop after this many consecutive task failures."`
	MaxTasks    *int           `name:"max-tasks" help:"Exit cleanly after processing this many tasks (0 = unlimited)."`
	Extra       []string       `arg:"" optional:"" hidden:""`
}

func (c *LoopCmd) Run(d *Deps, ctx context.Context) error {
	cfg := app.LoadLoopConfig()
	if c.Command != nil {
		cfg.Command = *c.Command
	}
	if c.Worker != nil {
		cfg.Worker = *c.Worker
	}
	if c.Lease != nil {
		cfg.Lease = *c.Lease
	}
	if c.Timeout != nil {
		cfg.TaskTimeout = *c.Timeout
	}
	if c.Cooldown != nil {
		cfg.Cooldown = *c.Cooldown
	}
	if c.Heartbeat != nil {
		cfg.HeartbeatInterval = *c.Heartbeat
	}
	if c.MaxFailures != nil {
		cfg.MaxConsecutiveFailures = *c.MaxFailures
	}
	if cfg.Worker == "" {
		cfg.Worker = fmt.Sprintf("loop-%d", os.Getpid())
	}
	if cfg.Command == "" {
		return fmt.Errorf("no agent command configured (set 'command' in ~/.config/afk/loop.yaml or pass --command)")
	}
	opts := app.LoopOptions{Worker: cfg.Worker}
	if c.MaxTasks != nil {
		opts.MaxTasks = *c.MaxTasks
	}
	return d.Service.RunLoop(ctx, cfg, opts, d.Stderr, d.Stderr, func(r app.LoopResult) error { return output.WriteJSONLine(d.Stdout, r, "loop") })
}
