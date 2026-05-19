package commands

import (
	"strings"

	"github.com/spf13/cobra"
)

func newEditCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <id> <body...>",
		Short: "Replace the body of a task",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Edit(cmd.Context(), args[0], strings.Join(args[1:], " "))
		},
	}
}

func newDoneCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task as done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Done(cmd.Context(), args[0])
		},
	}
}

func newFailCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "fail <id> <reason...>",
		Short: "Mark a task as failed with a reason",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Fail(cmd.Context(), args[0], strings.Join(args[1:], " "))
		},
	}
}

func newResetCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "reset <id>",
		Short: "Return a task to pending",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Reset(cmd.Context(), args[0])
		},
	}
}

func newRmCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove a task from the queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Remove(cmd.Context(), args[0])
		},
	}
}
