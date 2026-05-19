package commands

import (
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
	var blockedBy string
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Mark a task as blocked by another task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dependsOnID, err := normalizeBlockedBy(blockedBy, "")
			if err != nil {
				return err
			}
			if dependsOnID == "" {
				return nil
			}
			return d.Service.AddDependency(cmd.Context(), args[0], dependsOnID)
		},
	}
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "task id this task is blocked by")
	_ = cmd.MarkFlagRequired("blocked-by")
	return cmd
}

func newDepsRmCmd(d *Deps) *cobra.Command {
	var blockedBy string
	cmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove a blocked-by dependency",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dependsOnID, err := normalizeBlockedBy(blockedBy, "")
			if err != nil {
				return err
			}
			if dependsOnID == "" {
				return nil
			}
			return d.Service.RemoveDependency(cmd.Context(), args[0], dependsOnID)
		},
	}
	cmd.Flags().StringVar(&blockedBy, "blocked-by", "", "task id to unblock from")
	_ = cmd.MarkFlagRequired("blocked-by")
	return cmd
}

func newDepsLsCmd(d *Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls <id>",
		Short: "List task dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := d.Service.Dependencies(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return output.WriteDependencies(d.Stdout, deps, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}
