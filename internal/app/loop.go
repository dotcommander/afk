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

	"github.com/dotcommander/afk/internal/store"
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
}

// LoopResult records the outcome of a single task iteration. Emitted via the
// emit callback so the command layer can stream JSON lines.
type LoopResult struct {
	TaskID   string        `json:"task_id"`
	Status   string        `json:"status"` // "done" | "failed" | "timeout"
	Error    string        `json:"error,omitempty"`
	Attempt  int           `json:"attempt"`
	Duration time.Duration `json:"duration_ns"` // nanoseconds; cheap to record

	// TokensUsed is the count parsed from the bounded output tail for a durable
	// goal with a configured token cap. Agent output is still forwarded verbatim.
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
//
//nolint:gocognit,gocyclo,funlen // loop orchestration; sub-steps extracted to render/heartbeat/classify helpers
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
		goal, goalAttemptID, budgeted, limited, err := s.prepareGoalInvocation(ctx, t)
		if err != nil {
			return err
		}
		if limited {
			r := LoopResult{TaskID: t.ID, Status: "budget-limited", Error: goal.LimitReason, Attempt: attempt, Duration: time.Since(start)}
			if emitErr := emit(r); emitErr != nil {
				return emitErr
			}
			return nil
		}

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
		// Exit condition: hbCtx cancelled (via hbCancel called below).
		hbCancel := s.startHeartbeat(ctx, cfg, t.ID, worker)

		// --- Spawn agent ---
		stdout, stderr := agentOut, agentErr
		var stdoutTail, stderrTail *tailWriter
		if budgeted && goal.MaxTokens > 0 {
			stdoutTail = newTailWriter(agentOut, goalOutputTailLimit)
			stderrTail = newTailWriter(agentErr, goalOutputTailLimit)
			stdout, stderr = stdoutTail, stderrTail
		}
		runErr := runAgent(ctx, cfg.Command, prompt, cfg.TaskTimeout, stdout, stderr)

		// Stop heartbeat immediately when the task finishes.
		if hbCancel != nil {
			hbCancel()
		}

		// --- Classify result ---
		classification, tokens, goalLimited, err := s.finalizeLoopInvocation(ctx, t.ID, goalAttemptID, runErr, goal, budgeted, stdoutTail, stderrTail)
		if err != nil {
			return err
		}
		if classification.failed {
			consecutiveFailures++
		} else {
			consecutiveFailures = 0
		}

		tasksProcessed++

		r := LoopResult{
			TaskID:     t.ID,
			Status:     classification.status,
			Error:      classification.errText,
			Attempt:    attempt,
			Duration:   time.Since(start),
			TokensUsed: int(tokens),
		}
		if emitErr := emit(r); emitErr != nil {
			return emitErr
		}

		// --- Circuit breaker ---
		if cfg.MaxConsecutiveFailures > 0 && consecutiveFailures >= cfg.MaxConsecutiveFailures {
			return fmt.Errorf("circuit breaker: %d consecutive failures", consecutiveFailures)
		}

		if goalLimited {
			return nil
		}

		// --- Cooldown (interruptible) ---
		if err := s.cooldown(ctx, cfg.Cooldown); err != nil {
			return err
		}
	}
}

func (s *Service) finalizeLoopInvocation(ctx context.Context, taskID string, attemptID int64, runErr error, goal task.GoalGroup, budgeted bool, stdoutTail, stderrTail *tailWriter) (runClassification, int64, bool, error) {
	if !budgeted {
		classification, err := s.classifyRun(ctx, taskID, runErr)
		return classification, 0, false, err
	}
	classification := classifyAgentResult(runErr)
	tokens, available := int64(0), true
	if goal.MaxTokens > 0 {
		tokens, available = parseGoalTokens(goal.TokenRegex, stdoutTail.Bytes(), stderrTail.Bytes())
	}
	finalized, err := s.store.FinalizeGoalInvocation(ctx, store.GoalFinalization{
		TaskID: taskID, AttemptID: attemptID, Succeeded: !classification.failed, Error: classification.errText,
		TokensUsed: tokens, TokensAvailable: available, Now: s.now(),
	})
	if err != nil {
		return runClassification{}, 0, false, fmt.Errorf("finalize goal task %s: %w", taskID, err)
	}
	return classification, tokens, finalized.Limited, nil
}

// startHeartbeat launches a best-effort lease-heartbeat goroutine when
// cfg.HeartbeatInterval > 0 and returns its cancel func; returns nil when
// heartbeats are disabled. Exit condition: the returned cancel is invoked
// (cancels the goroutine's context).
func (s *Service) startHeartbeat(ctx context.Context, cfg LoopConfig, taskID, worker string) context.CancelFunc {
	if cfg.HeartbeatInterval <= 0 {
		return nil
	}
	hbCtx, hbCancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(cfg.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Best-effort: heartbeat errors are logged nowhere intentionally.
				// The lease will expire naturally if heartbeats fail consistently.
				_ = s.Heartbeat(hbCtx, taskID, worker, cfg.Lease)
			case <-hbCtx.Done():
				return
			}
		}
	}()
	return hbCancel
}

type runClassification struct {
	status  string
	errText string
	failed  bool
}

// classifyRun records the task's terminal state for this attempt. failed=true
// means it should count toward the consecutive-failure breaker. A non-nil
// error is fatal to the loop (store write failed).
func (s *Service) classifyRun(ctx context.Context, taskID string, runErr error) (runClassification, error) {
	classification := classifyAgentResult(runErr)
	switch classification.status {
	case "done":
		if doneErr := s.Done(ctx, taskID, "completed by afk loop"); doneErr != nil {
			return runClassification{}, fmt.Errorf("mark done %s: %w", taskID, doneErr)
		}
	case "timeout":
		if failErr := s.Fail(ctx, taskID, "timeout"); failErr != nil {
			return runClassification{}, fmt.Errorf("mark failed %s: %w", taskID, failErr)
		}
	default:
		if failErr := s.Fail(ctx, taskID, runErr.Error()); failErr != nil {
			return runClassification{}, fmt.Errorf("mark failed %s: %w", taskID, failErr)
		}
	}
	return classification, nil
}

func classifyAgentResult(runErr error) runClassification {
	switch {
	case runErr == nil:
		return runClassification{status: "done"}
	case errors.Is(runErr, ErrAgentTimeout):
		return runClassification{status: "timeout", errText: runErr.Error(), failed: true}
	default:
		return runClassification{status: "failed", errText: runErr.Error(), failed: true}
	}
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
