// Package commands wires cobra subcommands to actions for the afk CLI.
package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

func newAddCmd(d *Deps) *cobra.Command {
	var tags []string
	var priority string
	var cwd string
	var noCWD bool
	var source string
	var agent string
	var groupID string
	var resourceKey string
	var blockedBy string
	var after string
	var asJSON bool
	var dryRun bool
	var diagnose bool
	var force bool

	cmd := &cobra.Command{
		Use:   "add <body...>",
		Short: "Append a new pending task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAddCommand(cmd, d, addCommandInput{
				args:        args,
				tags:        tags,
				priority:    priority,
				cwd:         cwd,
				noCWD:       noCWD,
				source:      source,
				agent:       agent,
				groupID:     groupID,
				resourceKey: resourceKey,
				blockedBy:   blockedBy,
				after:       after,
			}, addCommandMode{
				asJSON:   asJSON,
				dryRun:   dryRun,
				diagnose: diagnose,
				force:    force,
			})
		},
	}
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "task tag (repeatable)")
	cmd.Flags().StringVar(&priority, "priority", "", "task priority")
	cmd.Flags().StringVar(&cwd, "cwd", "", "task working directory context (defaults to current directory)")
	cmd.Flags().BoolVar(&noCWD, "no-cwd", false, "do not record current working directory context")
	cmd.Flags().StringVar(&source, "source", "", "task source")
	cmd.Flags().StringVar(&agent, "agent", "", "preferred agent")
	cmd.Flags().StringVar(&groupID, "group", "", "task group id")
	cmd.Flags().StringVar(&resourceKey, "resource", "", "task resource key")
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "task id this task is blocked by, or none")
	cmd.Flags().StringVar(&after, "after", "", "alias for --blocked-by")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without adding a task")
	cmd.Flags().BoolVar(&diagnose, "diagnose", false, "run all validation checks and report every failure (read-only)")
	cmd.Flags().BoolVar(&force, "force", false, "Bypass validation rejection. Requires AFK_ALLOW_FORCE=1 in environment.")
	return cmd
}

type addCommandInput struct {
	args        []string
	tags        []string
	priority    string
	cwd         string
	noCWD       bool
	source      string
	agent       string
	groupID     string
	resourceKey string
	blockedBy   string
	after       string
}

type addCommandMode struct {
	asJSON   bool
	dryRun   bool
	diagnose bool
	force    bool
}

func runAddCommand(cmd *cobra.Command, d *Deps, input addCommandInput, mode addCommandMode) error {
	opts, dependsOnID, err := buildAddCommandOptions(input)
	if err != nil {
		return err
	}
	if mode.force && mode.diagnose {
		cmd.SilenceUsage = true
		return errors.New("--force and --diagnose are mutually exclusive")
	}
	if mode.diagnose {
		return runAddDiagnose(cmd, d, opts)
	}
	if mode.dryRun {
		if err := task.ValidateAddOptions(opts); err != nil {
			return err
		}
		return writeAddDryRunResult(d, mode.asJSON)
	}
	if mode.force {
		if v := os.Getenv("AFK_ALLOW_FORCE"); v != "1" {
			cmd.SilenceUsage = true
			return fmt.Errorf("--force requires AFK_ALLOW_FORCE=1 in environment (current: %q)", v)
		}
		fmt.Fprintln(d.Stderr, "warning: --force bypassing validation")
		id, err := d.Service.AddWithOptionsForce(cmd.Context(), opts)
		if err != nil {
			cmd.SilenceUsage = true
			return err
		}
		return writeAddResult(d, id, mode.asJSON)
	}
	if dependsOnID != "" {
		if _, err := d.Service.Show(cmd.Context(), dependsOnID); err != nil {
			return err
		}
	}
	id, err := d.Service.AddWithOptions(cmd.Context(), opts)
	if err != nil {
		return err
	}
	if dependsOnID != "" {
		if err := d.Service.AddDependency(cmd.Context(), id, dependsOnID); err != nil {
			return err
		}
	}
	return writeAddResult(d, id, mode.asJSON)
}

func buildAddCommandOptions(input addCommandInput) (task.AddOptions, string, error) {
	dependsOnID, err := normalizeBlockedBy(input.blockedBy, input.after)
	if err != nil {
		return task.AddOptions{}, "", err
	}
	resolvedCWD, err := resolveAddCWD(input.cwd, input.noCWD)
	if err != nil {
		return task.AddOptions{}, "", err
	}
	return task.AddOptions{
		Body:        strings.Join(input.args, " "),
		Priority:    input.priority,
		Tags:        input.tags,
		CWD:         resolvedCWD,
		Source:      input.source,
		Agent:       input.agent,
		GroupID:     input.groupID,
		ResourceKey: input.resourceKey,
	}, dependsOnID, nil
}

func resolveAddCWD(cwd string, noCWD bool) (string, error) {
	if cwd != "" || noCWD {
		return cwd, nil
	}
	resolvedCWD, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return resolvedCWD, nil
}

func writeAddDryRunResult(d *Deps, asJSON bool) error {
	if asJSON {
		return output.WriteJSONLine(d.Stdout, struct {
			Valid bool `json:"valid"`
		}{Valid: true}, "add dry-run")
	}
	_, err := fmt.Fprintln(d.Stdout, "valid")
	return err
}

func writeAddResult(d *Deps, id string, asJSON bool) error {
	if asJSON {
		return output.WriteJSONLine(d.Stdout, struct {
			ID string `json:"id"`
		}{ID: id}, "add")
	}
	_, err := fmt.Fprintln(d.Stdout, id)
	return err
}

func normalizeBlockedBy(blockedBy, after string) (string, error) {
	if blockedBy != "" && after != "" && blockedBy != after {
		return "", fmt.Errorf("--blocked-by and --after disagree")
	}
	value := blockedBy
	if value == "" {
		value = after
	}
	if value == "" || strings.EqualFold(value, "none") {
		return "", nil
	}
	return value, nil
}

// runAddDiagnose runs ValidateAddOptionsAll and reports every failure on its
// own stderr line. Read-only: never calls Service.AddWithOptions and so never
// writes a rejection sidecar entry or inserts a row.
func runAddDiagnose(cmd *cobra.Command, d *Deps, opts task.AddOptions) error {
	err := task.ValidateAddOptionsAll(opts)
	if err == nil {
		_, werr := fmt.Fprintln(d.Stdout, "task validates")
		return werr
	}
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		for _, e := range joined.Unwrap() {
			if _, werr := fmt.Fprintln(d.Stderr, e.Error()); werr != nil {
				return werr
			}
		}
	} else {
		if _, werr := fmt.Fprintln(d.Stderr, err.Error()); werr != nil {
			return werr
		}
	}
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return err
}
