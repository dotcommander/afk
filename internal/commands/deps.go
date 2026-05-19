package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
)

func newDepsCmd(d *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Manage task dependencies",
	}
	cmd.AddCommand(
		newDepsAddCmd(d),
		newDepsRmCmd(d),
		newDepsLsCmd(d),
	)
	return cmd
}

func newDepsAddCmd(d *Deps) *cobra.Command {
	return newDepsMutateCmd(
		"add <id>",
		"Mark a task as blocked by another task",
		"task id this task is blocked by",
		d.Service.AddDependency,
	)
}

func newDepsRmCmd(d *Deps) *cobra.Command {
	return newDepsMutateCmd(
		"rm <id>",
		"Remove a blocked-by dependency",
		"task id to unblock from",
		d.Service.RemoveDependency,
	)
}

func newDepsMutateCmd(use, short, flagUsage string, mutate func(context.Context, string, string) error) *cobra.Command {
	var blockedBy string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dependsOnID, err := normalizeBlockedBy(blockedBy, "")
			if err != nil {
				return err
			}
			if dependsOnID == "" {
				return nil
			}
			return mutate(cmd.Context(), args[0], dependsOnID)
		},
	}
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", flagUsage)
	_ = cmd.MarkFlagRequired("blocked-by")
	return cmd
}

func newDepsLsCmd(d *Deps) *cobra.Command {
	return newJSONByIDCmd("ls <id>", "List task dependencies", "emit JSON output", func(ctx context.Context, id string, asJSON bool) error {
		deps, err := d.Service.Dependencies(ctx, id)
		if err != nil {
			return err
		}
		return output.WriteDependencies(d.Stdout, deps, asJSON)
	})
}
