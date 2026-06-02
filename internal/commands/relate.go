package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dotcommander/afk/internal/task"
)

func newRelateCmd(d *Deps) *cobra.Command {
	var relType string
	cmd := &cobra.Command{
		Use:   "relate <task-id> <related-id>",
		Short: "Add a typed relation between two tasks",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := task.ParseRelationType(relType)
			if err != nil {
				return err
			}
			if err := d.Service.AddRelation(cmd.Context(), args[0], args[1], rt); err != nil {
				return err
			}
			_, err = fmt.Fprintf(d.Stdout, "Relation added: %s %s %s\n", args[0], rt, args[1])
			return err
		},
	}
	cmd.Flags().StringVar(&relType, "type", "blocks", "relation type (blocks, relates, duplicates, parent)")
	return cmd
}
