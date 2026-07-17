package commands

import (
	"context"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/task"
)

type TasksCmd struct {
	Status string   `help:"Filter by status."`
	Stage  string   `help:"Filter by pipeline stage."`
	JSON   bool     `help:"Emit JSONL output."`
	Extra  []string `arg:"" optional:"" hidden:""`
}

func (c *TasksCmd) Run(d *Deps, ctx context.Context) error {
	tasks, err := d.Service.List(ctx, c.Status)
	if err != nil {
		return err
	}
	return output.WriteList(d.Stdout, filterTasksByStage(tasks, c.Stage), c.JSON)
}

func filterTasksByStage(tasks []task.Task, stage string) []task.Task {
	if stage == "" {
		return tasks
	}
	out := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Stage == stage {
			out = append(out, t)
		}
	}
	return out
}

type FindCmd struct {
	Query  string `arg:"" required:""`
	Status string `help:"Filter by status."`
	JSON   bool   `help:"Emit JSONL output."`
}

func (c *FindCmd) Run(d *Deps, ctx context.Context) error {
	tasks, err := d.Service.Find(ctx, c.Query, c.Status)
	if err != nil {
		return err
	}
	return output.WriteList(d.Stdout, tasks, c.JSON)
}

type StatusCmd struct {
	JSON    bool     `help:"Emit JSON output."`
	Summary bool     `help:"Emit counts only."`
	Blocked bool     `help:"Include dependency-blocked todo task details."`
	Extra   []string `arg:"" optional:"" hidden:""`
}

func (c *StatusCmd) Run(d *Deps, ctx context.Context) error {
	snapshot, err := d.Service.Status(ctx)
	if err != nil {
		return err
	}
	if c.Summary {
		if c.JSON {
			return output.WriteCountJSON(d.Stdout, snapshot.Counts)
		}
		return output.WriteCount(d.Stdout, snapshot.Counts)
	}
	var blocked []task.BlockedTask
	if c.Blocked {
		blocked, err = d.Service.Blocked(ctx)
		if err != nil {
			return err
		}
		if blocked == nil {
			blocked = []task.BlockedTask{}
		}
	}
	return output.WriteStatus(d.Stdout, snapshot.Counts, snapshot.Todo, snapshot.Doing, blocked, snapshot.Health, c.JSON, d.Now())
}

type TaskCmd struct {
	ID   string `arg:"" required:""`
	JSON bool   `help:"Emit JSON output."`
}

func (c *TaskCmd) Run(d *Deps, ctx context.Context) error {
	data, err := d.Service.Explain(ctx, c.ID)
	if err != nil {
		return err
	}
	gates, err := d.Service.Gates(ctx, c.ID)
	if err != nil {
		return err
	}
	return output.WriteExplainWithGates(d.Stdout, data.Task, gates, data.Events, data.Attempts, c.JSON)
}
