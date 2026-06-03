package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/template"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// LoopOptions carries runtime overrides resolved by the command layer.
type LoopOptions struct {
	// MaxTasks caps how many tasks are processed before the loop exits cleanly.
	// 0 means unlimited.
	MaxTasks int

	// Worker is the worker identity string. When empty, RunLoop derives
	// "loop-<pid>" so the caller need not construct it.
	Worker string

	// GoalBudgetCheck, when non-nil, is consulted after each iteration for any
	// task carrying a GroupID. It accounts the iteration against the goal's
	// budget and returns the first exceeded cap (or BudgetOK). When nil, budget
	// checking is disabled and RunLoop behaves identically to a plain loop.
	GoalBudgetCheck func(groupID string, r LoopResult) (BudgetLimitReason, error)
}

// LoopResult records the outcome of a single task iteration. Emitted via the
// emit callback so the command layer can stream JSON lines.
type LoopResult struct {
	TaskID   string        `json:"task_id"`
	Status   string        `json:"status"` // "done" | "failed" | "timeout"
	Error    string        `json:"error,omitempty"`
	Attempt  int           `json:"attempt"`
	Duration time.Duration `json:"duration_ns"` // nanoseconds; cheap to record

	// TokensUsed is the token count parsed from the agent output for this
	// iteration. It stays 0 unless a token_regex is configured AND the loop has
	// access to parseable agent output; RunLoop forwards agent stdout/stderr
	// verbatim to agentOut/agentErr without capturing it, so RunLoop itself
	// leaves this 0. The goal loop populates it when it captures agent output.
	TokensUsed int `json:"tokens_used,omitempty"`
}

// RunLoop drives the autonomous task execution loop.
//
// It claims tasks from the queue, renders a prompt via cfg.PromptTemplate,
// spawns the configured agent command, and marks each task done or failed
// based on the agent's exit status.
//
// agentOut / agentErr are forwarded verbatim to the child process's stdout /
// stderr so the caller controls where agent output lands (e.g. os.Stdout,
// a log writer, or a test buffer).
//
// emit is called once per iteration with the result; if it returns an error
// RunLoop stops and returns it (sink broken).
//
// Exit conditions:
//   - Take returns nil task (empty queue) — clean exit, nil returned.
//   - opts.MaxTasks reached — clean exit, nil returned.
//   - Circuit breaker trips (cfg.MaxConsecutiveFailures > 0) — error returned.
//   - ctx is cancelled — ctx.Err() returned.
func (s *Service) RunLoop(
	ctx context.Context,
	cfg LoopConfig,
	opts LoopOptions,
	agentOut, agentErr io.Writer,
	emit func(LoopResult) error,
) error {
	worker := opts.Worker
	if worker == "" {
		worker = fmt.Sprintf("loop-%d", os.Getpid())
	}

	// Pre-parse the prompt template once so per-task rendering is cheap and
	// template errors are caught immediately, not buried in the first iteration.
	tmpl, err := template.New("prompt").Parse(cfg.PromptTemplate)
	if err != nil {
		return fmt.Errorf("parse prompt template: %w", err)
	}

	var (
		tasksProcessed      int
		consecutiveFailures int
		attempt             int
	)

	for {
		// Honour cancellation at loop top before any blocking work.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// MaxTasks guard — 0 means unlimited.
		if opts.MaxTasks > 0 && tasksProcessed >= opts.MaxTasks {
			return nil
		}

		// --- Claim next task ---
		t, err := s.Take(ctx, cfg.Lease, worker, worker)
		if err != nil {
			return fmt.Errorf("take task: %w", err)
		}
		if t == nil {
			// Queue empty — clean exit.
			return nil
		}

		attempt++
		start := time.Now()

		// --- Render prompt ---
		var buf bytes.Buffer
		if tmplErr := tmpl.Execute(&buf, t); tmplErr != nil {
			// Template render failure is a task-level error, not a loop error.
			_ = s.Fail(ctx, t.ID, fmt.Sprintf("prompt render: %v", tmplErr))
			consecutiveFailures++
			r := LoopResult{
				TaskID:   t.ID,
				Status:   "failed",
				Error:    tmplErr.Error(),
				Attempt:  attempt,
				Duration: time.Since(start),
			}
			if emitErr := emit(r); emitErr != nil {
				return emitErr
			}
			if cfg.MaxConsecutiveFailures > 0 && consecutiveFailures >= cfg.MaxConsecutiveFailures {
				return fmt.Errorf("circuit breaker: %d consecutive failures", consecutiveFailures)
			}
			if err := s.cooldown(ctx, cfg.Cooldown); err != nil {
				return err
			}
			continue
		}
		prompt := buf.String()

		// --- Heartbeat goroutine ---
		// Exit condition: hbCtx cancelled (via hbCancel deferred below).
		var hbCancel context.CancelFunc
		if cfg.HeartbeatInterval > 0 {
			var hbCtx context.Context
			hbCtx, hbCancel = context.WithCancel(ctx)
			go func() {
				ticker := time.NewTicker(cfg.HeartbeatInterval)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						// Best-effort: heartbeat errors are logged nowhere intentionally.
						// The lease will expire naturally if heartbeats fail consistently.
						_ = s.Heartbeat(hbCtx, t.ID, worker, cfg.Lease)
					case <-hbCtx.Done():
						return
					}
				}
			}()
		}

		// --- Spawn agent ---
		runErr := runAgent(ctx, cfg.Command, prompt, cfg.TaskTimeout, agentOut, agentErr)

		// Stop heartbeat immediately when the task finishes.
		if hbCancel != nil {
			hbCancel()
		}

		// --- Classify result ---
		var (
			status  string
			errText string
		)
		switch {
		case runErr == nil:
			if doneErr := s.Done(ctx, t.ID, "completed by afk loop"); doneErr != nil {
				return fmt.Errorf("mark done %s: %w", t.ID, doneErr)
			}
			status = "done"
			consecutiveFailures = 0

		case errors.Is(runErr, ErrAgentTimeout):
			if failErr := s.Fail(ctx, t.ID, "timeout"); failErr != nil {
				return fmt.Errorf("mark failed %s: %w", t.ID, failErr)
			}
			status = "timeout"
			errText = runErr.Error()
			consecutiveFailures++

		default:
			if failErr := s.Fail(ctx, t.ID, runErr.Error()); failErr != nil {
				return fmt.Errorf("mark failed %s: %w", t.ID, failErr)
			}
			status = "failed"
			errText = runErr.Error()
			consecutiveFailures++
		}

		tasksProcessed++

		r := LoopResult{
			TaskID:   t.ID,
			Status:   status,
			Error:    errText,
			Attempt:  attempt,
			Duration: time.Since(start),
		}
		if emitErr := emit(r); emitErr != nil {
			return emitErr
		}

		// --- Circuit breaker ---
		if cfg.MaxConsecutiveFailures > 0 && consecutiveFailures >= cfg.MaxConsecutiveFailures {
			return fmt.Errorf("circuit breaker: %d consecutive failures", consecutiveFailures)
		}

		// --- Goal budget (optional; disabled when GoalBudgetCheck == nil) ---
		// Only consulted for grouped tasks. On any exceeded cap, suspend the
		// remaining group tasks (budget-limited) and exit cleanly so no new task
		// is claimed.
		halt, budgetErr := s.applyGoalBudget(ctx, t, opts, r)
		if budgetErr != nil {
			return budgetErr
		}
		if halt {
			return nil
		}

		// --- Cooldown (interruptible) ---
		if err := s.cooldown(ctx, cfg.Cooldown); err != nil {
			return err
		}
	}
}

// applyGoalBudget consults opts.GoalBudgetCheck for grouped tasks. On any
// exceeded cap it suspends the remaining group tasks (budget-limited) and
// reports halt=true so RunLoop exits cleanly without claiming a new task.
// Returns halt=false for ungrouped tasks or when no check is configured.
func (s *Service) applyGoalBudget(ctx context.Context, t *task.Task, opts LoopOptions, r LoopResult) (halt bool, err error) {
	if t.GroupID == "" || opts.GoalBudgetCheck == nil {
		return false, nil
	}
	reason, budgetErr := opts.GoalBudgetCheck(t.GroupID, r)
	if budgetErr != nil {
		return false, fmt.Errorf("goal budget check: %w", budgetErr)
	}
	if reason != BudgetOK {
		if limitErr := s.store.BudgetLimitGroup(ctx, t.GroupID); limitErr != nil {
			return false, fmt.Errorf("budget-limit group %s: %w", t.GroupID, limitErr)
		}
		return true, nil
	}
	return false, nil
}

// cooldown sleeps for d interruptibly. Returns ctx.Err() on cancellation,
// nil immediately if d <= 0.
func (s *Service) cooldown(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
