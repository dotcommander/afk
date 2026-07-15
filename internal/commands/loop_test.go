package commands

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoopCommandFailsClosedWithNoCommand verifies that `afk loop` returns a
// clear error when no agent command is configured and no --command flag is
// passed. This exercises the fail-closed guard in newLoopCmd without touching
// the user's real ~/.config/afk/loop.yaml.
//
// The loop.yaml singleton is loaded once per process via sync.Once, so we
// cannot reset it between tests. Instead we pass --command "" explicitly;
// an empty string is equivalent to no command configured.
func TestLoopCommandFailsClosedWithNoCommand(t *testing.T) {
	t.Parallel()

	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	d := testDepsWithWriters(stdout, stderr)

	root := newTestCLI(d, "test")
	root.SetArgs([]string{"--queue", queuePath, "loop", "--command", ""})
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no agent command configured")
}
