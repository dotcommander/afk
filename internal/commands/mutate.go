package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/task"
)

func newSetCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "set <id> <status> [note...]",
		Short: "Set task status",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, ok := task.ParseStatus(args[1])
			if !ok {
				return fmt.Errorf("%w: %q", task.ErrInvalidStatus, args[1])
			}
			return d.Service.SetStatus(cmd.Context(), args[0], status, strings.Join(args[2:], " "))
		},
	}
}
