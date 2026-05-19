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

Mine concrete AFK-ready candidate tasks from the target repo without mutating the queue.

Start with:
  git status --porcelain=v2
  rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
  afk count
  afk ready
  afk ls --status failed --json
  afk ls --status working --json

Accept only candidates with:
  - current evidence from files, command output, tests, or docs/source mismatch
  - atomic scope that a worker can complete independently
  - exact verification command
  - low churn risk and no broad cleanup/refactor wording
  - no duplicate pending or working task for the same behavior

Candidate bodies should start with [discovery:<repo>:<topic>] and include Evidence:, Scope:, and Verify with.
Use afk add --dry-run --source task-discovery --tag discovery before enqueueing generated candidates.
`
