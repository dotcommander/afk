package commands

import (
	"context"
	"fmt"
)

type GateCmd struct {
	Add     GateAddCmd     `cmd:"" help:"Add a named gate to a task."`
	Satisfy GateSatisfyCmd `cmd:"" help:"Mark a task gate satisfied."`
}
type GateAddCmd struct {
	ID   string `arg:"" required:""`
	Name string `arg:"" required:""`
}

func (c *GateAddCmd) Run(d *Deps, ctx context.Context) error {
	if err := d.Service.AddGate(ctx, c.ID, c.Name); err != nil {
		return err
	}
	_, err := fmt.Fprintf(d.Stdout, "gate add %s %s\n", c.ID, c.Name)
	return err
}

type GateSatisfyCmd struct {
	ID   string `arg:"" required:""`
	Name string `arg:"" required:""`
}

func (c *GateSatisfyCmd) Run(d *Deps, ctx context.Context) error {
	if err := d.Service.SatisfyGate(ctx, c.ID, c.Name); err != nil {
		return err
	}
	_, err := fmt.Fprintf(d.Stdout, "gate satisfy %s %s\n", c.ID, c.Name)
	return err
}
