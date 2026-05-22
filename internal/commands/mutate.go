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

func newRetryCmd(d *Deps) *cobra.Command {
	var asJSON bool
	var reason string

	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Open a new attempt for a failed task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			note := retryNote(reason)
			if err := d.Service.SetStatus(cmd.Context(), args[0], task.StatusWorking, note); err != nil {
				return err
			}
			if asJSON {
				return output.WriteJSONLine(d.Stdout, setResult{
					ID:     args[0],
					Status: task.StatusWorking,
					Note:   note,
				}, "retry")
			}
			_, err := fmt.Fprintf(d.Stdout, "retry %s doing\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "one-line reason the task is ready to retry")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}

func retryNote(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "retrying"
	}
	return "retrying: " + reason
}

type setResult struct {
	ID     string      `json:"id"`
	Status task.Status `json:"status"`
	Note   string      `json:"note,omitzero"`
}
