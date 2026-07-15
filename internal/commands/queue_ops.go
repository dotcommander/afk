package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

// takeKind is the command name and the JSON "kind" token emitted for claimed tasks.
const takeKind = "take"

// reapKind is the command name for the cron-driven stale-task reaper.
const reapKind = "reap"

type TakeCmd struct {
	Lease        string   `help:"Lease duration for the claim (for example 30m)."`
	Worker       string   `help:"Worker id for the claim."`
	DryRun       bool     `name:"dry-run" help:"Preview ready tasks without claiming."`
	Limit        int      `default:"20" help:"Maximum ready tasks to print with --dry-run."`
	JSON         bool     `default:"true" help:"emit JSONL output; enabled by default."`
	Summary      bool     `help:"Include queue counts with the claimed task or dry-run preview."`
	Full         bool     `help:"Include full task bodies in JSON output."`
	Envelope     bool     `help:"Emit a stable JSON object envelope."`
	Task         string   `help:"Claim this exact ready task id."`
	SatisfyGates []string `name:"satisfy-gate" sep:"none" help:"Gate to satisfy atomically with --task (repeatable)."`
	Extra        []string `arg:"" optional:"" hidden:""`
}

func (c *TakeCmd) Run(d *Deps, ctx context.Context) error {
	leaseDuration, err := parseOptionalDuration("lease", c.Lease)
	if err != nil {
		return err
	}
	if c.DryRun {
		if c.Task != "" || len(c.SatisfyGates) > 0 {
			return fmt.Errorf("--task/--satisfy-gate cannot be combined with --dry-run")
		}
		return runTakeDryRun(ctx, d, takeDryRunOptions{limit: c.Limit, asJSON: c.JSON, summary: c.Summary, full: c.Full, envelope: c.Envelope})
	}
	return runTakeClaim(ctx, d, leaseDuration, c.Worker, c.Task, c.SatisfyGates, c.Summary, c.Full, c.Envelope)
}

type takeDryRunOptions struct {
	limit    int
	asJSON   bool
	summary  bool
	full     bool
	envelope bool
}

func runTakeDryRun(ctx context.Context, d *Deps, opts takeDryRunOptions) error {
	ready, err := d.Service.Ready(ctx)
	if err != nil {
		return err
	}
	readyCount := len(ready)
	if opts.limit > 0 && len(ready) > opts.limit {
		ready = ready[:opts.limit]
	}
	bodyLimit, bodyHint := takePreviewBodyPolicy(opts.full)
	if opts.envelope || opts.summary {
		snapshot, err := d.Service.Status(ctx)
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

func runTakeClaim(ctx context.Context, d *Deps, leaseDuration time.Duration, workerID, taskID string, satisfyGates []string, summary, full, envelope bool) error {
	if len(satisfyGates) > 0 && taskID == "" {
		return fmt.Errorf("--satisfy-gate requires --task")
	}
	var claimed *task.Task
	var err error
	if taskID != "" {
		claimed, err = d.Service.TakeTask(ctx, taskID, leaseDuration, workerID, workerID, satisfyGates)
	} else {
		claimed, err = d.Service.Take(ctx, leaseDuration, workerID, "")
	}
	if err != nil {
		return err
	}
	if claimed == nil {
		return writeNoReadyExplanation(ctx, d)
	}
	if err := task.ValidateAddOptions(task.AddOptionsFromTask(*claimed)); err != nil {
		if failErr := d.Service.Fail(ctx, claimed.ID, err.Error()); failErr != nil {
			return fmt.Errorf("take %s: invalid claimed task %v; auto-fail: %w", claimed.ID, err, failErr)
		}
		return fmt.Errorf("take %s: %w", claimed.ID, err)
	}
	if summary || envelope {
		return writeTakeSummary(ctx, d, *claimed, full)
	}
	if full {
		return output.WriteBoundTaskJSONLine(d.Stdout, *claimed, 0, takeKind)
	}
	return output.WriteTaskJSONLine(d.Stdout, *claimed, takeKind)
}

func writeTakeSummary(ctx context.Context, d *Deps, claimed task.Task, full bool) error {
	snapshot, err := d.Service.Status(ctx)
	if err != nil {
		return err
	}
	ready, err := d.Service.Ready(ctx)
	if err != nil {
		return err
	}
	if full {
		return output.WriteTakeSummaryFull(d.Stdout, claimed, snapshot.Counts, len(ready))
	}
	return output.WriteTakeSummary(d.Stdout, claimed, snapshot.Counts, len(ready))
}

func writeNoReadyExplanation(ctx context.Context, d *Deps) error {
	e, err := d.Service.ExplainNotReady(ctx)
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

type RequeueStaleCmd struct {
	OlderThan string   `name:"older-than" default:"1h" help:"Requeue doing tasks older than this duration when no lease is set."`
	Extra     []string `arg:"" optional:"" hidden:""`
}

func (c *RequeueStaleCmd) Run(d *Deps, ctx context.Context) error {
	if err := warnDeprecated(d.Stderr, "afk requeue-stale", "afk task <id>, afk set <id> failed --note <reason>, then afk add <replacement task>"); err != nil {
		return err
	}
	dur, err := parseRequiredDuration("older-than", c.OlderThan)
	if err != nil {
		return err
	}
	return runRequeueStale(ctx, d, dur)
}

type ReapCmd struct {
	OlderThan string   `name:"older-than" default:"20m" help:"Requeue doing tasks older than this duration when no lease is set."`
	Extra     []string `arg:"" optional:"" hidden:""`
}

func (c *ReapCmd) Run(d *Deps, ctx context.Context) error {
	dur, err := parseRequiredDuration("older-than", c.OlderThan)
	if err != nil {
		return err
	}
	return runRequeueStale(ctx, d, dur)
}

func runRequeueStale(ctx context.Context, d *Deps, olderThan time.Duration) error {
	tasks, err := d.Service.RequeueStale(ctx, olderThan)
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

type HeartbeatCmd struct {
	ID     string `arg:"" required:""`
	Worker string `required:"" help:"Worker id that owns the claim."`
	Lease  string `default:"30m" help:"New lease duration from now."`
}

func (c *HeartbeatCmd) Run(d *Deps, ctx context.Context) error {
	if err := warnDeprecated(d.Stderr, "afk heartbeat", "afk take --lease <duration> with a long enough lease"); err != nil {
		return err
	}
	leaseDuration, err := parseOptionalDuration("lease", c.Lease)
	if err != nil {
		return err
	}
	return d.Service.Heartbeat(ctx, c.ID, c.Worker, leaseDuration)
}
