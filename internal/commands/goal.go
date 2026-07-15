package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// GoalCmd owns the `afk goal` command family.
// GoalConfig, applies flag overrides, compiles the contract via the setup agent,
// and (unless --dry-run/--json) gates insertion behind interactive approval.
// All workflow logic lives in internal/app.
type GoalCmd struct {
	Create GoalCreateCmd `cmd:"" default:"withargs" hidden:"" help:"Compile an objective into an approved task contract and queue it."`
	Status GoalStatusCmd `cmd:"" help:"Show a goal group's status and task summary."`
	Audit  GoalAuditCmd  `cmd:"" help:"Run the independent completion auditor on a task."`
	Resume GoalResumeCmd `cmd:"" help:"Resume a limited goal with optional budget overrides."`
}

type GoalCreateCmd struct {
	SetupCommand  *string        `name:"setup-command" help:"Agent command template for contract compilation."`
	AuditCommand  *string        `name:"audit-command" help:"Agent command template for the independent auditor."`
	MaxTokens     *int64         `name:"max-tokens" help:"Per-goal token budget (0 = unlimited)."`
	MaxIterations *int64         `name:"max-iterations" help:"Per-goal iteration cap (0 = unlimited)."`
	MaxDuration   *time.Duration `name:"max-duration" help:"Per-goal wall-clock cap (0 = unlimited)."`
	TokenRegex    *string        `name:"token-regex" help:"Regex with one capture group for token usage."`
	DryRun        bool           `name:"dry-run" help:"Print the contract and exit without queueing tasks."`
	JSON          bool           `help:"Print the contract as JSON and skip interactive approval."`
	CWD           string         `help:"Working directory for queued tasks (default: current directory)."`
	Objective     string         `arg:"" required:""`
}

func (c *GoalCreateCmd) Run(d *Deps, ctx context.Context) error {
	cfg := goalConfigWithOverrides(goalOverrideFlags{setupCommand: c.SetupCommand, auditCommand: c.AuditCommand, maxTokens: c.MaxTokens, maxIters: c.MaxIterations, maxDuration: c.MaxDuration, tokenRegex: c.TokenRegex})
	return runGoalCmd(ctx, d, c.Objective, cfg, c.DryRun, c.JSON, c.CWD)
}

type goalOverrideFlags struct {
	setupCommand *string
	auditCommand *string
	maxTokens    *int64
	maxIters     *int64
	maxDuration  *time.Duration
	tokenRegex   *string
}

// goalConfigWithOverrides loads goal.yaml and applies only explicitly supplied
// flag overrides, mirroring loop.go's flag.Changed pattern.
func goalConfigWithOverrides(overrides goalOverrideFlags) app.GoalConfig {
	cfg := app.LoadGoalConfig()
	if overrides.setupCommand != nil {
		cfg.SetupCommand = *overrides.setupCommand
	}
	if overrides.auditCommand != nil {
		cfg.AuditCommand = *overrides.auditCommand
	}
	if overrides.maxTokens != nil {
		cfg.MaxTokens = int(*overrides.maxTokens)
	}
	if overrides.maxIters != nil {
		cfg.MaxIterations = int(*overrides.maxIters)
	}
	if overrides.maxDuration != nil {
		cfg.MaxDuration = *overrides.maxDuration
	}
	if overrides.tokenRegex != nil {
		cfg.TokenRegex = *overrides.tokenRegex
	}
	return cfg
}

// runGoalCmd drives compile → present → approve → insert. --dry-run and --json
// both print the contract and return without queueing (--json is the scripted,
// non-interactive path); interactive mode prompts on Stderr and reads Stdin.
func runGoalCmd(ctx context.Context, d *Deps, objective string, cfg app.GoalConfig, dryRun, asJSON bool, cwd string) error {
	if len([]rune(objective)) > 4000 {
		return app.ErrGoalObjectiveTooLong
	}
	if err := app.ValidateGoalConfig(cfg); err != nil {
		return err
	}

	var setupBuf bytes.Buffer
	contract, err := d.Service.RunGoalSetup(ctx, cfg, objective, &setupBuf, d.Stderr)
	if err != nil {
		return err
	}

	if dryRun || asJSON {
		return output.WriteJSONLine(d.Stdout, contract, "goal")
	}

	if err := output.WriteJSONLine(d.Stdout, contract, "goal"); err != nil {
		return err
	}
	_, _ = fmt.Fprint(d.Stderr, "Approve contract? [yes/no]: ")
	var answer string
	if _, err := fmt.Fscan(d.Stdin, &answer); err != nil || strings.ToLower(strings.TrimSpace(answer)) != "yes" {
		_, _ = fmt.Fprintln(d.Stderr, "goal declined by user")
		return app.ErrGoalDeclined
	}

	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	goalID := uuid.NewString()
	if err := d.Service.CreateGoal(ctx, goalID, objective, contract, cwd, cfg); err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, struct {
		GoalID string `json:"goal_id"`
		Tasks  int    `json:"tasks"`
	}{GoalID: goalID, Tasks: len(contract.Tasks)}, "goal-receipt")
}

// newGoalStatusCmd shows a goal group's durable record and member task counts.
type GoalStatusCmd struct {
	GoalID string `arg:"" required:""`
}

func (c *GoalStatusCmd) Run(d *Deps, ctx context.Context) error {
	goalID := c.GoalID
	group, err := d.Service.GetGoalGroup(ctx, goalID)
	if err != nil {
		return err
	}
	counts, err := d.Service.CountTasksByGroupID(ctx, goalID)
	if err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, struct {
		ID         string         `json:"id"`
		Objective  string         `json:"objective"`
		Outcome    string         `json:"outcome"`
		Status     string         `json:"status"`
		CreatedAt  string         `json:"created_at"`
		GroupID    string         `json:"group_id"`
		TaskCounts map[string]int `json:"task_counts"`
		Budget     goalBudgetJSON `json:"budget"`
	}{
		ID:         group.ID,
		Objective:  group.Objective,
		Outcome:    group.Outcome,
		Status:     group.Status,
		CreatedAt:  group.CreatedAt,
		GroupID:    group.GroupID,
		TaskCounts: counts,
		Budget:     goalBudgetFromGroup(group),
	}, "goal-status")
}

type goalBudgetJSON struct {
	MaxTokens      int64         `json:"max_tokens"`
	MaxIterations  int64         `json:"max_iterations"`
	MaxDuration    time.Duration `json:"max_duration_ns"`
	TokenRegex     string        `json:"token_regex"`
	TokensUsed     int64         `json:"tokens_used"`
	IterationsUsed int64         `json:"iterations_used"`
	EpochStarted   string        `json:"epoch_started"`
	Reason         string        `json:"reason"`
	LimitedAt      string        `json:"limited_at"`
}

func goalBudgetFromGroup(g task.GoalGroup) goalBudgetJSON {
	return goalBudgetJSON{MaxTokens: g.MaxTokens, MaxIterations: g.MaxIterations, MaxDuration: g.MaxDuration,
		TokenRegex: g.TokenRegex, TokensUsed: g.TokensUsed, IterationsUsed: g.IterationsUsed,
		EpochStarted: g.BudgetEpochStarted, Reason: g.LimitReason, LimitedAt: g.LimitedAt}
}

type GoalResumeCmd struct {
	MaxTokens     *int64         `name:"max-tokens" help:"Per-goal token budget (0 = unlimited)."`
	MaxIterations *int64         `name:"max-iterations" help:"Per-goal iteration cap (0 = unlimited)."`
	MaxDuration   *time.Duration `name:"max-duration" help:"Per-goal wall-clock cap (0 = unlimited)."`
	TokenRegex    *string        `name:"token-regex" help:"Regex with one capture group for token usage."`
	GoalID        string         `arg:"" required:""`
}

func (c *GoalResumeCmd) Run(d *Deps, ctx context.Context) error {
	var changes store.GoalResumeChanges
	changes.MaxTokens = c.MaxTokens
	changes.MaxIterations = c.MaxIterations
	changes.MaxDuration = c.MaxDuration
	changes.TokenRegex = c.TokenRegex
	result, err := d.Service.ResumeGoal(ctx, c.GoalID, changes)
	if err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, struct {
		GoalID       string         `json:"goal_id"`
		ResumedTasks int            `json:"resumed_tasks"`
		Budget       goalBudgetJSON `json:"budget"`
	}{GoalID: c.GoalID, ResumedTasks: result.ResumedTasks, Budget: goalBudgetFromGroup(result.Goal)}, "goal-resume")
}

// newGoalAuditCmd manually invokes the independent auditor on a task and, on
// disapproval, re-queues it.
type GoalAuditCmd struct {
	AuditCommand *string `name:"audit-command" help:"Agent command template for the auditor."`
	TaskID       string  `arg:"" required:""`
}

func (c *GoalAuditCmd) Run(d *Deps, ctx context.Context) error {
	taskID := c.TaskID
	cfg := app.LoadGoalConfig()
	if c.AuditCommand != nil {
		cfg.AuditCommand = *c.AuditCommand
	}
	t, err := d.Service.Show(ctx, taskID)
	if err != nil {
		return err
	}
	res, err := d.Service.RunGoalAudit(ctx, cfg, t.GroupID, taskID, t.Body, d.Stderr, d.Stderr)
	if err != nil {
		return err
	}
	if res.Disapproved {
		if err := d.Service.RequeueAfterAuditDisapproval(ctx, taskID, "auditor disapproved"); err != nil {
			return err
		}
	}
	return output.WriteJSONLine(d.Stdout, res, "goal-audit")
}
