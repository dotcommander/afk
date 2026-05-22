package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

func newSetCmd(d *Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "set <id> <status> [note...]",
		Short: "Set task status",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, ok := task.ParseStatus(args[1])
			if !ok {
				return fmt.Errorf("%w: %q", task.ErrInvalidStatus, args[1])
			}
			note := strings.Join(args[2:], " ")
			if err := d.Service.SetStatus(cmd.Context(), args[0], status, note); err != nil {
				return err
			}
			if asJSON {
				return output.WriteJSONLine(d.Stdout, setResult{
					ID:     args[0],
					Status: status,
					Note:   note,
				}, "set")
			}
			_, err := fmt.Fprintf(d.Stdout, "set %s %s\n", args[0], status)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}

type setResult struct {
	ID     string      `json:"id"`
	Status task.Status `json:"status"`
	Note   string      `json:"note,omitzero"`
}
