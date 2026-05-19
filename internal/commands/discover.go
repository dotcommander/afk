package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDiscoverCmd(d *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Print task-discovery workflow guidance",
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

const discoverStubText = `afk discover is a workflow stub.

Use docs/task-discovery.md to mine concrete AFK-ready candidate tasks.

Start with:
  git status --porcelain=v2
  rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
  afk count
  afk ready
  afk ls --status failed --json
  afk ls --status working --json

Candidate bodies should start with [discovery:<repo>:<topic>] and include Evidence:, Scope:, and Verify with.
`
