package commands

import (
	"strings"

	"github.com/spf13/cobra"
)

func newBlockCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "block <id> <reason...>",
		Short: "Block a task from scheduling",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Block(cmd.Context(), args[0], strings.Join(args[1:], " "))
		},
	}
}

func newUnblockCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "unblock <id>",
		Short: "Remove a manual task block",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.Service.Unblock(cmd.Context(), args[0])
		},
	}
}
