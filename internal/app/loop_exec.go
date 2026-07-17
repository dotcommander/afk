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

	sqlite3 "modernc.org/sqlite/lib"
)

// ErrAgentTimeout is returned by runAgent when the child process exceeds its
// allotted timeout.
var ErrAgentTimeout = errors.New("agent process timed out")

// ErrLeaseLost reports that loop execution stopped because its worker no
// longer held a renewable lease. The task is deliberately left unfinalized so
// a new owner can recover it without a stale worker overwriting that result.
var ErrLeaseLost = errors.New("task lease lost")

type heartbeatMonitor struct {
	cancel context.CancelFunc
	done   chan struct{}
	lost   chan error
}

type leaseWatch struct {
	deadline time.Time
	timer    *time.Timer
}

func newLeaseWatch(now func() time.Time, lease time.Duration, persisted string) (*leaseWatch, error) {
	if lease <= 0 {
		return nil, nil
	}
	persistedDeadline, err := time.Parse(time.RFC3339, persisted)
	if err != nil {
		return nil, fmt.Errorf("invalid persisted lease deadline %q", persisted)
	}
	remaining := persistedDeadline.Sub(now())
	return &leaseWatch{
		deadline: time.Now().Add(remaining),
		timer:    time.NewTimer(remaining),
	}, nil
}

func (w *leaseWatch) expired() <-chan time.Time {
	if w == nil {
		return nil
	}
	return w.timer.C
}

func (w *leaseWatch) valid() bool {
	return w == nil || time.Now().Before(w.deadline)
}

func (w *leaseWatch) renewed(lease time.Duration) {
	if w == nil {
		return
	}
	w.deadline = time.Now().Add(lease)
	w.timer.Reset(lease)
}

func (w *leaseWatch) stop() {
	if w != nil {
		w.timer.Stop()
	}
}

func (m *heartbeatMonitor) stop() error {
	if m == nil {
		return nil
	}
	m.cancel()
	<-m.done
	select {
	case err := <-m.lost:
		return err
	default:
		return nil
	}
}

// startHeartbeat maintains the worker's lease and cancels execution when the
// lease is definitively lost. SQLite busy/locked errors are transient while
// the last confirmed lease remains valid; other renewal errors fail closed.
func (s *Service) startHeartbeat(ctx context.Context, cfg LoopConfig, taskID, worker, claimedLeaseExpires string, cancelExecution context.CancelFunc) *heartbeatMonitor {
	if cfg.HeartbeatInterval <= 0 && cfg.Lease <= 0 {
		return nil
	}
	hbCtx, hbCancel := context.WithCancel(ctx)
	monitor := &heartbeatMonitor{
		cancel: hbCancel,
		done:   make(chan struct{}),
		lost:   make(chan error, 1),
	}
	go s.monitorHeartbeat(hbCtx, cfg, taskID, worker, claimedLeaseExpires, cancelExecution, monitor)
	return monitor
}

func (s *Service) monitorHeartbeat(ctx context.Context, cfg LoopConfig, taskID, worker, claimedLeaseExpires string, cancelExecution context.CancelFunc, monitor *heartbeatMonitor) {
	defer close(monitor.done)
	var heartbeat <-chan time.Time
	var ticker *time.Ticker
	if cfg.HeartbeatInterval > 0 {
		ticker = time.NewTicker(cfg.HeartbeatInterval)
		heartbeat = ticker.C
		defer ticker.Stop()
	}
	watch, err := newLeaseWatch(s.now, cfg.Lease, claimedLeaseExpires)
	if err != nil {
		reportLeaseLoss(monitor, cancelExecution, err)
		return
	}
	defer watch.stop()
	for {
		select {
		case <-heartbeat:
			err := s.renewHeartbeat(ctx, cfg.Lease, taskID, worker, watch)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				watch.renewed(cfg.Lease)
				continue
			}
			if isSQLiteContention(err) && watch.valid() {
				continue
			}
			reportLeaseLoss(monitor, cancelExecution, err)
			return
		case <-watch.expired():
			reportLeaseLoss(monitor, cancelExecution, context.DeadlineExceeded)
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) renewHeartbeat(ctx context.Context, lease time.Duration, taskID, worker string, watch *leaseWatch) error {
	renewCtx := ctx
	cancel := func() {}
	if watch != nil {
		renewCtx, cancel = context.WithDeadline(ctx, watch.deadline)
	}
	defer cancel()
	return s.Heartbeat(renewCtx, taskID, worker, lease)
}

func reportLeaseLoss(monitor *heartbeatMonitor, cancelExecution context.CancelFunc, err error) {
	monitor.lost <- fmt.Errorf("%w: %w", ErrLeaseLost, err)
	cancelExecution()
}

func isSQLiteContention(err error) bool {
	var coded interface{ Code() int }
	if !errors.As(err, &coded) {
		return false
	}
	switch coded.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

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
