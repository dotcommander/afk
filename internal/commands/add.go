// Package commands wires cobra subcommands to actions for the afk CLI.
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

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

	cmd := &cobra.Command{
		Use:   "add <body...>",
		Short: "Append a new pending task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedCWD := cwd
			if resolvedCWD == "" && !noCWD {
				var err error
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
	return cmd
}
