package app

// Independent completion auditor (Decision 4 / Phase E). The auditor is a
// separate agent invocation from the worker: it inspects real artifacts against
// the goal's recorded objective and emits a terminal <approved/>/<disapproved/>
// marker. Disapproval re-queues the task. Extracted from goal.go to keep both
// files under the 300-line tripwire.

import (
	"context"
	"io"

	"github.com/dotcommander/afk/internal/task"
)

// RunGoalAudit invokes AuditCommand with an audit prompt derived from cfg's
// AuditPromptTemplate and the goal group's objective, then parses the captured
// output for the <approved/>/<disapproved/> terminal markers.
//
// Fail-safe: a timed-out or errored audit NEVER yields Approved=true. On
// timeout or agent error the result is recorded as a disapproval with
// AuditResult.Error set; a non-nil error is returned only for infra failures
// that prevented the audit from running (empty command, prompt build error,
// objective lookup failure).
//
// Exit conditions: ctx cancelled, AuditTimeout exceeded, agent error.
func (s *Service) RunGoalAudit(
	ctx context.Context,
	cfg GoalConfig,
	goalID, _, completionNote string,
	auditOut, auditErr io.Writer,
) (AuditResult, error) {
	if cfg.AuditCommand == "" {
		return AuditResult{}, ErrGoalAuditNotConfigured
	}

	// Objective for the audit frame comes from the durable goal group, not the
	// caller, so the auditor judges against the raw user objective (the contract
	// outcome is retained separately in group.Outcome for reference).
	var objective string
	if goalID != "" {
		group, err := s.store.GetGoalGroup(ctx, goalID)
		if err != nil {
			return AuditResult{}, err
		}
		objective = group.Objective
	}

	prompt, err := buildAuditPrompt(cfg, objective, completionNote)
	if err != nil {
		return AuditResult{}, err
	}

	output, runErr := runGoalAuditAgent(ctx, cfg, prompt, auditOut, auditErr)
	if runErr != nil {
		// Fail-safe: any execution failure (timeout, agent error) is a
		// disapproval, never an approval.
		return AuditResult{Disapproved: true, Output: output, Error: runErr.Error()}, nil
	}

	approved, disapproved := parseAuditDecision(output)
	return AuditResult{Approved: approved, Disapproved: disapproved, Output: output}, nil
}

// RequeueAfterAuditDisapproval returns a disapproved task to todo with the
// auditor summary as the event message, re-entering the readiness predicate
// normally.
func (s *Service) RequeueAfterAuditDisapproval(ctx context.Context, taskID, summary string) error {
	return s.SetStatus(ctx, taskID, task.StatusTodo, summary)
}
