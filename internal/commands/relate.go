package commands

import (
	"context"
	"fmt"
	"github.com/dotcommander/afk/internal/task"
)

type RelateCmd struct {
	TaskID    string `arg:"" required:""`
	RelatedID string `arg:"" required:""`
	Type      string `default:"blocks" help:"Relation type (blocks, relates, duplicates, parent)."`
}

func (c *RelateCmd) Run(d *Deps, ctx context.Context) error {
	rt, err := task.ParseRelationType(c.Type)
	if err != nil {
		return err
	}
	if err = d.Service.AddRelation(ctx, c.TaskID, c.RelatedID, rt); err != nil {
		return err
	}
	_, err = fmt.Fprintf(d.Stdout, "Relation added: %s %s %s\n", c.TaskID, rt, c.RelatedID)
	return err
}
