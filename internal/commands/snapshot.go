package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dotcommander/afk/internal/output"
)

type SnapshotCmd struct {
	Label      string   `help:"Snapshot label, for example before or after."`
	TaskID     string   `name:"task" help:"Include task details, events, and attempts."`
	OutputPath string   `name:"output" help:"Write snapshot JSON to path instead of stdout."`
	Extra      []string `arg:"" optional:"" hidden:""`
}

func (c *SnapshotCmd) Run(d *Deps, ctx context.Context) error {
	w, closeFn, err := snapshotWriter(d.Stdout, c.OutputPath)
	if err != nil {
		return err
	}
	if err := writeSnapshot(ctx, d, w, c.Label, c.TaskID); err != nil {
		_ = closeFn()
		return err
	}
	return closeFn()
}

func writeSnapshot(ctx context.Context, d *Deps, w io.Writer, label, taskID string) error {
	snapshot, err := d.Service.Status(ctx)
	if err != nil {
		return err
	}
	ready, err := d.Service.Ready(ctx)
	if err != nil {
		return err
	}
	var detail *output.SnapshotTaskDetail
	if taskID != "" {
		data, err := d.Service.Explain(ctx, taskID)
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
	return output.WriteSnapshot(w, output.SnapshotData{
		Label:   label,
		Created: now.Format(time.RFC3339),
		Counts:  snapshot.Counts,
		Ready:   ready,
		Todo:    snapshot.Todo,
		Doing:   snapshot.Doing,
		Health:  snapshot.Health,
		Detail:  detail,
		Now:     now,
	})
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
