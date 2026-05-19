package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/task"
)

func newDoctorCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check queue health and installation basics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasks, err := d.Service.List(cmd.Context(), "")
			if err != nil {
				return err
			}
			tally := map[task.Status]int{}
			var staleWorking int
			for _, t := range tasks {
				tally[t.Status]++
				if t.Status == task.StatusWorking {
					staleWorking++
				}
			}
			if _, err := fmt.Fprintf(d.Stdout, "queue: %s\n", d.QueuePaths.SQLitePath); err != nil {
				return err
			}
			if _, err := os.Stat(d.QueuePaths.SQLitePath); err != nil {
				if os.IsNotExist(err) {
					if _, err := fmt.Fprintln(d.Stdout, "db: missing"); err != nil {
						return err
					}
				} else {
					return fmt.Errorf("stat queue db: %w", err)
				}
			} else if _, err := fmt.Fprintln(d.Stdout, "db: ok"); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(d.Stdout, "pending: %d\nworking: %d\ndone: %d\nfailed: %d\n", tally[task.StatusPending], tally[task.StatusWorking], tally[task.StatusDone], tally[task.StatusFailed]); err != nil {
				return err
			}
			if staleWorking > 0 {
				if _, err := fmt.Fprintf(d.Stdout, "working tasks: %d (inspect with afk ls --status working --json)\n", staleWorking); err != nil {
					return err
				}
			}
			exe, err := os.Executable()
			if err != nil {
				exe = "unknown"
			}
			_, err = fmt.Fprintf(d.Stdout, "binary: %s\nprompt: ok\n", exe)
			return err
		},
	}
}
