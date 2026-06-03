package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"text/template"
	"time"
)

// ErrAgentTimeout is returned by runAgent when the child process exceeds its
// allotted timeout.
var ErrAgentTimeout = errors.New("agent process timed out")

// runAgent builds argv from commandTmpl with .Prompt = prompt, spawns the
// resulting command as a child process, and waits for it to exit. stdout and
// stderr from the child are forwarded to the supplied writers.
//
// Argv construction: the TEMPLATE is tokenized with strings.Fields first
// (operator-authored, controlled whitespace), then each token is rendered
// independently. This ensures a token like {{.Prompt}} expands to exactly one
// argv element regardless of spaces or newlines in the prompt. Shell-style
// quoting WITHIN the template is not supported — use the {{.Prompt}} token
// form rather than shell quotes around the placeholder.
//
// Timeout + kill escalation: the child receives SIGTERM when timeout elapses;
// if it does not exit within WaitDelay seconds the runtime escalates to SIGKILL
// (Go 1.20+ Cmd.Cancel / WaitDelay semantics).
//
// (loop.yaml / --command flag), never from task data.
//
//nolint:gosec // G204: command template originates from operator config
func runAgent(ctx context.Context, commandTmpl, prompt string, timeout time.Duration, stdout, stderr io.Writer) error {
	argv, err := buildArgv(commandTmpl, prompt)
	if err != nil {
		return fmt.Errorf("build argv: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, argv[0], argv[1:]...)

	// SIGTERM first; runtime escalates to SIGKILL after WaitDelay if process
	// does not exit.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Stdin is nil intentionally — headless, non-interactive agent driver.

	if runErr := cmd.Run(); runErr != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%w: %w", ErrAgentTimeout, runErr)
		}
		return fmt.Errorf("agent exited with error: %w", runErr)
	}
	return nil
}

// buildArgv tokenizes commandTmpl with strings.Fields, then renders each token
// independently through text/template with .Prompt = prompt. Each token becomes
// exactly one argv element, so {{.Prompt}} expands to a single element even
// when the prompt contains spaces or newlines.
func buildArgv(commandTmpl, prompt string) ([]string, error) {
	tokens := strings.Fields(commandTmpl)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("command template is empty")
	}
	data := struct{ Prompt string }{Prompt: prompt}
	argv := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tmpl, err := template.New("tok").Parse(tok)
		if err != nil {
			return nil, fmt.Errorf("parse token %q: %w", tok, err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("render token %q: %w", tok, err)
		}
		argv = append(argv, buf.String())
	}
	return argv, nil
}
