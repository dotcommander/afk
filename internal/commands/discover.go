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
		Use:    "discover",
		Short:  "Print task-discovery workflow guidance",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		Annotations: map[string]string{
			"skipStoreInit": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return fmt.Errorf("afk discover has moved; use afk prompt --discover")
		},
	}
	return cmd
}

func writeDiscoverPrompt(d *Deps) error {
	_, err := fmt.Fprint(d.Stdout, discoverStubText)
	return err
}
