package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/app"
)

// newRejectedCmd builds the `afk rejected` subcommand tree for inspecting
// and re-driving the rejection sidecar populated by AddWithOptions when
// validation fails.
func newRejectedCmd(d *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rejected",
		Short: "Inspect and re-drive validation-rejected tasks",
		Long: "Tasks that fail validation are recorded in <queue-dir>/rejected.jsonl. " +
			"Use these subcommands to list, inspect, retry, or discard them.",
	}
	cmd.AddCommand(
		newRejectedLsCmd(d),
		newRejectedShowCmd(d),
		newRejectedRetryCmd(d),
		newRejectedRmCmd(d),
	)
	return cmd
}

func newRejectedLsCmd(d *Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List rejected tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			records, err := d.Service.ListRejected()
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(d.Stdout)
				for _, rec := range records {
					if err := enc.Encode(rec); err != nil {
						return err
					}
				}
				return nil
			}
			if len(records) == 0 {
				fmt.Fprintln(d.Stdout, "no rejected tasks")
				return nil
			}
			for i, rec := range records {
				excerpt := firstLine(rec.Body)
				if len(excerpt) > 72 {
					excerpt = excerpt[:72] + "..."
				}
				fmt.Fprintf(d.Stdout, "%d  %s  %s\n      reason: %s\n",
					i+1,
					rec.Ts.Format("2006-01-02 15:04:05"),
					excerpt,
					rec.Reason,
				)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit one JSON record per line")
	return cmd
}

func newRejectedShowCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show <index>",
		Short: "Show a single rejected task in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := parseRejectedIndex(args[0])
			if err != nil {
				return err
			}
			records, err := d.Service.ListRejected()
			if err != nil {
				return err
			}
			if idx < 0 || idx >= len(records) {
				cmd.SilenceUsage = true
				return app.ErrRejectionIndexOutOfRange
			}
			rec := records[idx]
			fmt.Fprintf(d.Stdout, "index:  %d\n", idx+1)
			fmt.Fprintf(d.Stdout, "ts:     %s\n", rec.Ts.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(d.Stdout, "reason: %s\n", rec.Reason)
			if rec.Source != "" {
				fmt.Fprintf(d.Stdout, "source: %s\n", rec.Source)
			}
			if rec.CWD != "" {
				fmt.Fprintf(d.Stdout, "cwd:    %s\n", rec.CWD)
			}
			if len(rec.Tags) > 0 {
				fmt.Fprintf(d.Stdout, "tags:   %s\n", strings.Join(rec.Tags, ", "))
			}
			fmt.Fprintln(d.Stdout, "body:")
			fmt.Fprintln(d.Stdout, rec.Body)
			return nil
		},
	}
}

func newRejectedRetryCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "retry <index>",
		Short: "Re-attempt validation on a rejected task; on success it is added and removed from the sidecar",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := parseRejectedIndex(args[0])
			if err != nil {
				return err
			}
			created, err := d.Service.RetryRejected(cmd.Context(), idx)
			if err != nil {
				cmd.SilenceUsage = true
				return err
			}
			fmt.Fprintf(d.Stdout, "retried as task %s\n", created.ID)
			return nil
		},
	}
}

func newRejectedRmCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <index>",
		Short: "Discard a rejected task from the sidecar",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := parseRejectedIndex(args[0])
			if err != nil {
				return err
			}
			removed, err := d.Service.RemoveRejected(idx)
			if err != nil {
				cmd.SilenceUsage = true
				return err
			}
			fmt.Fprintf(d.Stdout, "removed rejection %d (%s)\n", idx+1, removed.Reason)
			return nil
		},
	}
}

// parseRejectedIndex converts 1-based user input to 0-based internal index.
func parseRejectedIndex(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("index must be an integer: %w", err)
	}
	if n < 1 {
		return 0, errors.New("index must be 1 or greater")
	}
	return n - 1, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
