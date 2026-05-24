package commands

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/output"
)

func newSnapshotCmd(d *Deps) *cobra.Command {
	var label string
	var taskID string
	var outputPath string

	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Export a read-only queue evidence snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w, closeFn, err := snapshotWriter(d.Stdout, outputPath)
			if err != nil {
				return err
			}
			if err := writeSnapshot(cmd, d, w, label, taskID); err != nil {
				_ = closeFn()
				return err
			}
			return closeFn()
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "snapshot label, for example before or after")
	cmd.Flags().StringVar(&taskID, "task", "", "include task details, events, and attempts")
	cmd.Flags().StringVar(&outputPath, "output", "", "write snapshot JSON to path instead of stdout")
	return cmd
}

func writeSnapshot(cmd *cobra.Command, d *Deps, w io.Writer, label, taskID string) error {
	snapshot, err := d.Service.Status(cmd.Context())
	if err != nil {
		return err
	}
	ready, err := d.Service.Ready(cmd.Context())
	if err != nil {
		return err
	}
	var detail *output.SnapshotTaskDetail
	if taskID != "" {
		data, err := d.Service.Explain(cmd.Context(), taskID)
		if err != nil {
			return err
		}
		detail = &output.SnapshotTaskDetail{
			Task:     data.Task,
			Events:   data.Events,
			Attempts: data.Attempts,
		}
	}
	now := d.Now().UTC()
	return output.WriteSnapshot(w, label, now.Format(time.RFC3339), snapshot.Counts, ready, snapshot.Todo, snapshot.Doing, detail, now)
}

func snapshotWriter(stdout io.Writer, path string) (io.Writer, func() error, error) {
	if path == "" {
		return stdout, func() error { return nil }, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, func() error { return nil }, fmt.Errorf("snapshot output: %w", err)
	}
	return f, f.Close, nil
}
