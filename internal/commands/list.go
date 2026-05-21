package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
)

func newLsCmd(d *Deps) *cobra.Command {
	var status string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasks, err := d.Service.List(cmd.Context(), status)
			if err != nil {
				return err
			}
			return output.WriteList(d.Stdout, tasks, asJSON)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSONL output")
	return cmd
}

func newShowCmd(d *Deps) *cobra.Command {
	return newJSONByIDCmd("show <id>", "Show a single task", "emit JSON output", func(ctx context.Context, id string, asJSON bool) error {
		t, err := d.Service.Show(ctx, id)
		if err != nil {
			return err
		}
		return output.WriteShow(d.Stdout, t, asJSON)
	})
}

func newJSONByIDCmd(use, short, jsonUsage string, run func(context.Context, string, bool) error) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), args[0], asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, jsonUsage)
	return cmd
}

func newCountCmd(d *Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:    "count",
		Short:  "Print per-status tallies",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := warnDeprecated(d.Stderr, "afk count", "afk status"); err != nil {
				return err
			}
			tally, err := d.Service.Count(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return output.WriteCountJSON(d.Stdout, tally)
			}
			return output.WriteCount(d.Stdout, tally)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}

func newStatusCmd(d *Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print a queue snapshot: tallies plus pending and working tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			snapshot, err := d.Service.Status(cmd.Context())
			if err != nil {
				return err
			}
			return output.WriteStatus(d.Stdout, snapshot.Counts, snapshot.Pending, snapshot.Working, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}

func newNextCmd(d *Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:    "next",
		Short:  "Print the first pending task as JSON",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := warnDeprecated(d.Stderr, "afk next", "afk ready --limit 1 --json"); err != nil {
				return err
			}
			next, err := d.Service.Next(cmd.Context())
			if err != nil {
				return err
			}
			if next == nil {
				if asJSON {
					if _, err := fmt.Fprintln(d.Stdout, "{}"); err != nil {
						return fmt.Errorf("next: write: %w", err)
					}
				}
				return nil
			}
			return output.WriteTaskJSONLine(d.Stdout, *next, "next")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output (empty queue emits {})")
	return cmd
}

func newExplainCmd(d *Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "explain <id>",
		Short: "Show task state, events, and attempts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := d.Service.Explain(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return output.WriteExplain(d.Stdout, data.Task, data.Events, data.Attempts, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}
