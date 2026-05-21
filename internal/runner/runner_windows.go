//go:build windows

package runner

import (
	"context"
	"os/exec"
	"strconv"
)

func newShellCommand(ctx context.Context, commandText string) *exec.Cmd {
	//nolint:gosec // afk intentionally executes the user-provided --exec shell template.
	cmd := exec.CommandContext(ctx, "cmd.exe", "/C", commandText)
	// Default CommandContext cancel only signals the shell; children (afk done,
	// agent CLIs) would survive. Use taskkill /T to walk the process tree.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	return cmd
}
