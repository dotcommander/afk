package commands

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/output"
)

// newGoalCmd is the thin cobra wrapper for `afk goal "<objective>"`: it loads
// GoalConfig, applies flag overrides, compiles the contract via the setup agent,
// and (unless --dry-run/--json) gates insertion behind interactive approval.
// All workflow logic lives in internal/app.
func newGoalCmd(d *Deps) *cobra.Command {
	var (
		setupCommand string
		auditCommand string
		maxTokens    int
		maxIters     int
		maxDuration  time.Duration
		dryRun       bool
		asJSON       bool
		cwd          string
	)
	cmd := &cobra.Command{
		Use:   "goal <objective>",
		Short: "Compile an objective into an approved task contract and queue it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := goalConfigWithOverrides(cmd, setupCommand, auditCommand, maxTokens, maxIters, maxDuration)
			return runGoalCmd(cmd, d, args[0], cfg, dryRun, asJSON, cwd)
		},
	}
	cmd.Flags().StringVar(&setupCommand, "setup-command", "", "agent command template for contract compilation")
	cmd.Flags().StringVar(&auditCommand, "audit-command", "", "agent command template for the independent auditor")
	cmd.Flags().IntVar(&maxTokens, "max-tokens", 0, "per-goal token budget (0 = unlimited)")
	cmd.Flags().IntVar(&maxIters, "max-iterations", 0, "per-goal iteration cap (0 = unlimited)")
	cmd.Flags().DurationVar(&maxDuration, "max-duration", 0, "per-goal wall-clock cap (0 = unlimited)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the contract and exit without queueing tasks")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the contract as JSON and skip interactive approval")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory for queued tasks (default: current directory)")
	cmd.AddCommand(newGoalStatusCmd(d), newGoalAuditCmd(d))
	return cmd
}

// goalConfigWithOverrides loads goal.yaml and applies the Decision 8 flag
// overrides, mirroring loop.go's flag.Changed pattern.
func goalConfigWithOverrides(cmd *cobra.Command, setupCommand, auditCommand string, maxTokens, maxIters int, maxDuration time.Duration) app.GoalConfig {
	cfg := app.LoadGoalConfig()
	if cmd.Flags().Changed("setup-command") {
		cfg.SetupCommand = setupCommand
	}
	if cmd.Flags().Changed("audit-command") {
		cfg.AuditCommand = auditCommand
	}
	if cmd.Flags().Changed("max-tokens") {
		cfg.MaxTokens = maxTokens
	}
	if cmd.Flags().Changed("max-iterations") {
		cfg.MaxIterations = maxIters
	}
	if cmd.Flags().Changed("max-duration") {
		cfg.MaxDuration = maxDuration
	}
	return cfg
}

// runGoalCmd drives compile → present → approve → insert. --dry-run and --json
// both print the contract and return without queueing (--json is the scripted,
// non-interactive path); interactive mode prompts on Stderr and reads Stdin.
func runGoalCmd(cmd *cobra.Command, d *Deps, objective string, cfg app.GoalConfig, dryRun, asJSON bool, cwd string) error {
	if len([]rune(objective)) > 4000 {
		return app.ErrGoalObjectiveTooLong
	}

	var setupBuf bytes.Buffer
	contract, err := d.Service.RunGoalSetup(cmd.Context(), cfg, objective, &setupBuf, d.Stderr)
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
	if err := d.Service.InsertGoalTasks(cmd.Context(), goalID, contract, cwd); err != nil {
		return err
	}
	if err := d.Service.RecordGoalGroup(cmd.Context(), goalID, objective, contract); err != nil {
		return err
	}
	return output.WriteJSONLine(d.Stdout, struct {
		GoalID string `json:"goal_id"`
		Tasks  int    `json:"tasks"`
	}{GoalID: goalID, Tasks: len(contract.Tasks)}, "goal-receipt")
}

// newGoalStatusCmd shows a goal group's durable record and member task counts.
func newGoalStatusCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "status <goalID>",
		Short: "Show a goal group's status and task summary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			goalID := args[0]
			group, err := d.Service.GetGoalGroup(cmd.Context(), goalID)
			if err != nil {
				return err
			}
			tasks, err := d.Service.List(cmd.Context(), "all")
			if err != nil {
				return err
			}
			counts := map[string]int{}
			for _, t := range tasks {
				if t.GroupID == goalID {
					counts[string(t.Status)]++
				}
			}
			return output.WriteJSONLine(d.Stdout, struct {
				ID         string         `json:"id"`
				Objective  string         `json:"objective"`
				Outcome    string         `json:"outcome"`
				Status     string         `json:"status"`
				CreatedAt  string         `json:"created_at"`
				GroupID    string         `json:"group_id"`
				TaskCounts map[string]int `json:"task_counts"`
			}{
				ID:         group.ID,
				Objective:  group.Objective,
				Outcome:    group.Outcome,
				Status:     group.Status,
				CreatedAt:  group.CreatedAt,
				GroupID:    group.GroupID,
				TaskCounts: counts,
			}, "goal-status")
		},
	}
}

// newGoalAuditCmd manually invokes the independent auditor on a task and, on
// disapproval, re-queues it.
func newGoalAuditCmd(d *Deps) *cobra.Command {
	var auditCommand string
	cmd := &cobra.Command{
		Use:   "audit <taskID>",
		Short: "Run the independent completion auditor on a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]
			cfg := app.LoadGoalConfig()
			if cmd.Flags().Changed("audit-command") {
				cfg.AuditCommand = auditCommand
			}
			t, err := d.Service.Show(cmd.Context(), taskID)
			if err != nil {
				return err
			}
			res, err := d.Service.RunGoalAudit(cmd.Context(), cfg, t.GroupID, taskID, t.Body, d.Stderr, d.Stderr)
			if err != nil {
				return err
			}
			if res.Disapproved {
				if err := d.Service.RequeueAfterAuditDisapproval(cmd.Context(), taskID, "auditor disapproved"); err != nil {
					return err
				}
			}
			return output.WriteJSONLine(d.Stdout, res, "goal-audit")
		},
	}
	cmd.Flags().StringVar(&auditCommand, "audit-command", "", "agent command template for the auditor")
	return cmd
}
