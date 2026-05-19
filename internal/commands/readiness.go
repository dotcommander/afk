package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/output"
)

func newReadyCmd(d *Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List tasks ready to run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasks, err := d.Service.Ready(cmd.Context())
			if err != nil {
				return err
			}
			return output.WriteList(d.Stdout, tasks, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSONL output")
	return cmd
}

func newWhyCmd(d *Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "why <id>",
		Short: "Explain task readiness",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := d.Service.Why(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if asJSON {
				return output.WriteJSONLine(d.Stdout, data, "why")
			}
			return writeWhy(d.Stdout, data)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}

func writeWhy(w interface{ Write([]byte) (int, error) }, data app.ReadinessData) error {
	if _, err := fmt.Fprintf(w, "ID: %s\nReady: %t\n", data.Task.ID, data.Ready); err != nil {
		return fmt.Errorf("why: write: %w", err)
	}
	if len(data.Reasons) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Reasons:"); err != nil {
		return fmt.Errorf("why: write: %w", err)
	}
	for _, reason := range data.Reasons {
		if _, err := fmt.Fprintf(w, "  %s\n", readinessReasonText(reason)); err != nil {
			return fmt.Errorf("why: write: %w", err)
		}
	}
	return nil
}

func readinessReasonText(reason app.NotReadyReason) string {
	switch reason.Kind {
	case "dependency_pending":
		return "waiting on task " + reason.Detail
	case "dependency_working":
		return "waiting on working task " + reason.Detail
	case "dependency_failed":
		return "blocked by failed task " + reason.Detail
	case "dependency_missing":
		return "blocked by missing task " + reason.Detail
	case "manual_block":
		return "blocked: " + reason.Detail
	case "resource_locked":
		return "resource active on task " + reason.Detail
	default:
		return reason.Kind + " " + reason.Detail
	}
}
