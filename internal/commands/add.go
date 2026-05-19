// Package commands wires cobra subcommands to actions for the afk CLI.
package commands

import (
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

	cmd := &cobra.Command{
		Use:   "add <body...>",
		Short: "Append a new pending task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, dependsOnID, err := buildAddCommandOptions(addCommandInput{
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
			})
			if err != nil {
				return err
			}
			if dryRun {
				if err := task.ValidateAddOptions(opts); err != nil {
					return err
				}
				return writeAddDryRunResult(d, asJSON)
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
			return writeAddResult(d, id, asJSON)
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
