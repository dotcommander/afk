//go:build !windows

package runner

import (
	"context"
	"os/exec"
	"syscall"
)

func newShellCommand(ctx context.Context, commandText string) *exec.Cmd {
	//nolint:gosec // afk intentionally executes the user-provided --exec shell template.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", commandText)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd
}
