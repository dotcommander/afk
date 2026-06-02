package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newGateCmd(d *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate",
		Short: "Manage task gates",
	}
	cmd.AddCommand(
		newGateAddCmd(d),
		newGateSatisfyCmd(d),
	)
	return cmd
}

func newGateAddCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "add <id> <name>",
		Short: "Add a named gate to a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := d.Service.AddGate(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, err := fmt.Fprintf(d.Stdout, "gate add %s %s\n", args[0], args[1])
			return err
		},
	}
}

func newGateSatisfyCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "satisfy <id> <name>",
		Short: "Mark a task gate satisfied",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := d.Service.SatisfyGate(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, err := fmt.Fprintf(d.Stdout, "gate satisfy %s %s\n", args[0], args[1])
			return err
		},
	}
}
