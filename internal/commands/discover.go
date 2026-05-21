package commands

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed discover_stub.md
var discoverStubText string

func newDiscoverCmd(d *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Print task-discovery workflow guidance",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			cmd.SilenceUsage = true
			return fmt.Errorf("afk discover accepts no arguments; run it from the target path and follow the printed workflow")
		},
		Annotations: map[string]string{
			"skipStoreInit": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(d.Stdout, discoverStubText)
			return err
		},
	}
	return cmd
}
