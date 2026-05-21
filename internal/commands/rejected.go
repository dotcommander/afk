package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		Use:    "rejected",
		Short:  "Inspect and re-drive validation-rejected tasks",
		Hidden: true,
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
		RunE: func(_ *cobra.Command, _ []string) error {
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
			return writeRejectedList(d.Stdout, records)
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
			return writeRejectedShow(d.Stdout, idx+1, records[idx])
		},
	}
}

// writeRejectedList writes the human-readable listing of all rejection records.
// Empty slice emits the "no rejected tasks" sentinel line.
func writeRejectedList(w io.Writer, records []app.RejectionRecord) error {
	if len(records) == 0 {
		if _, err := fmt.Fprintln(w, "no rejected tasks"); err != nil {
			return err
		}
		return nil
	}
	for i, rec := range records {
		excerpt := firstLine(rec.Body)
		if len(excerpt) > 72 {
			excerpt = excerpt[:72] + "..."
		}
		if _, err := fmt.Fprintf(w, "%d  %s  %s\n      reason: %s\n",
			i+1,
			rec.Ts.Format("2006-01-02 15:04:05"),
			excerpt,
			rec.Reason,
		); err != nil {
			return err
		}
	}
	return nil
}

// writeRejectedShow writes the full detail of a single rejection record to w.
// displayIndex is 1-based (already incremented by the caller).
func writeRejectedShow(w io.Writer, displayIndex int, rec app.RejectionRecord) error {
	if _, err := fmt.Fprintf(w, "index:  %d\n", displayIndex); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "ts:     %s\n", rec.Ts.Format("2006-01-02 15:04:05")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "reason: %s\n", rec.Reason); err != nil {
		return err
	}
	if rec.Source != "" {
		if _, err := fmt.Fprintf(w, "source: %s\n", rec.Source); err != nil {
			return err
		}
	}
	if rec.CWD != "" {
		if _, err := fmt.Fprintf(w, "cwd:    %s\n", rec.CWD); err != nil {
			return err
		}
	}
	if len(rec.Tags) > 0 {
		if _, err := fmt.Fprintf(w, "tags:   %s\n", strings.Join(rec.Tags, ", ")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "body:"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, rec.Body); err != nil {
		return err
	}
	return nil
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
			_, err = fmt.Fprintf(d.Stdout, "retried as task %s\n", created.ID)
			return err
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
			_, err = fmt.Fprintf(d.Stdout, "removed rejection %d (%s)\n", idx+1, removed.Reason)
			return err
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
