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

	cmd := &cobra.Command{
		Use:   "add <body...>",
		Short: "Append a new pending task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dependsOnID, err := normalizeBlockedBy(blockedBy, after)
			if err != nil {
				return err
			}
			if dependsOnID != "" {
				if _, err := d.Service.Show(cmd.Context(), dependsOnID); err != nil {
					return err
				}
			}
			resolvedCWD := cwd
			if resolvedCWD == "" && !noCWD {
				resolvedCWD, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve cwd: %w", err)
				}
			}
			id, err := d.Service.AddWithOptions(cmd.Context(), task.AddOptions{
				Body:        strings.Join(args, " "),
				Priority:    priority,
				Tags:        tags,
				CWD:         resolvedCWD,
				Source:      source,
				Agent:       agent,
				GroupID:     groupID,
				ResourceKey: resourceKey,
			})
			if err != nil {
				return err
			}
			if dependsOnID != "" {
				if err := d.Service.AddDependency(cmd.Context(), id, dependsOnID); err != nil {
					return err
				}
			}
			if asJSON {
				return output.WriteJSONLine(d.Stdout, struct {
					ID string `json:"id"`
				}{ID: id}, "add")
			}
			_, err = fmt.Fprintln(d.Stdout, id)
			return err
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
	return cmd
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
