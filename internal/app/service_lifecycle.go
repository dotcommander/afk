package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// This file holds Service methods that transition task lifecycle state or
// manage worker claims. Some method names are legacy internal API names kept
// for existing callers; the public CLI now exposes these through `afk set` and
// `afk take`.

// Done marks a task done with an optional completion note.
func (s *Service) Done(ctx context.Context, id, note string) error {
	if strings.TrimSpace(note) == "" {
		return task.ErrMissingCompletionNote
	}
	return s.store.Update(ctx, id, task.EventDone, note, func(t *task.Task) bool {
		return t.MarkDone(s.now())
	})
}

// Fail marks a task failed with reason.
func (s *Service) Fail(ctx context.Context, id, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return task.ErrMissingCompletionNote
	}
	return s.store.Update(ctx, id, task.EventFailed, reason, func(t *task.Task) bool {
		return t.MarkFailed(s.now(), reason)
	})
}

// SetStatus moves a task to status and records message as lifecycle context.
func (s *Service) SetStatus(ctx context.Context, id string, status task.Status, message string) error {
	return s.setStatus(ctx, id, status, message, nil, false, "")
}

// SetStatusWithStage moves a task to status and, when stage is non-nil, also
// updates the free-form pipeline stage in the same atomic Update. A nil stage
// leaves the existing stage unchanged.
func (s *Service) SetStatusWithStage(ctx context.Context, id string, status task.Status, message string, stage *string) error {
	return s.setStatus(ctx, id, status, message, stage, false, "")
}

// SetStatusWithStageWorker applies a worker-fenced lifecycle transition.
func (s *Service) SetStatusWithStageWorker(ctx context.Context, id string, status task.Status, message string, stage *string, workerID string) error {
	_, err := s.SetStatusWithStageWorkerTask(ctx, id, status, message, stage, false, workerID)
	return err
}

// SetStatusWithStageWorkerTask applies a worker-fenced transition and returns its committed snapshot.
func (s *Service) SetStatusWithStageWorkerTask(ctx context.Context, id string, status task.Status, message string, stage *string, force bool, workerID string) (task.Task, error) {
	status, ok := task.ParseStatus(string(status))
	if !ok {
		return task.Task{}, task.ErrInvalidStatus
	}
	if !force && isTerminalStatus(status) && strings.TrimSpace(message) == "" {
		return task.Task{}, task.ErrMissingCompletionNote
	}
	return s.store.UpdateFencedTask(ctx, id, workerID, eventForStatus(status), message, func(t *task.Task) bool {
		return s.applyStatusAndStage(t, status, message, stage)
	})
}

// SetStatusWithStageForce is the explicit escape hatch for terminal
// transitions without completion evidence. Public callers should prefer
// SetStatusWithStage; CLI --force is the intended use for this method.
func (s *Service) SetStatusWithStageForce(ctx context.Context, id string, status task.Status, message string, stage *string) error {
	return s.setStatus(ctx, id, status, message, stage, true, "")
}

// SetStatusWithRequest performs a lifecycle mutation exactly once.
func (s *Service) SetStatusWithRequest(ctx context.Context, actor, requestID, id string, status task.Status, message string, stage *string, force bool) (task.Task, bool, error) {
	hasRequestID := requestID != ""
	actor = strings.TrimSpace(actor)
	requestID = strings.TrimSpace(requestID)
	if hasRequestID && requestID == "" {
		return task.Task{}, false, errors.New("request id must not be blank")
	}
	if requestID == "" {
		err := s.setStatus(ctx, id, status, message, stage, force, "")
		if err != nil {
			return task.Task{}, false, err
		}
		updated, err := s.store.Get(ctx, id)
		return updated, false, err
	}
	status, ok := task.ParseStatus(string(status))
	if !ok {
		return task.Task{}, false, task.ErrInvalidStatus
	}
	if !force && isTerminalStatus(status) && strings.TrimSpace(message) == "" {
		return task.Task{}, false, task.ErrMissingCompletionNote
	}
	requested, ok := s.store.(interface {
		UpdateRequested(context.Context, string, string, string, string, task.EventType, string, func(*task.Task) bool) (task.Task, bool, error)
	})
	if !ok {
		return task.Task{}, false, errors.New("request id is unsupported by this store")
	}
	operation, err := requestOperation("task.set", struct {
		ID      string      `json:"id"`
		Status  task.Status `json:"status"`
		Message string      `json:"message"`
		Stage   *string     `json:"stage,omitempty"`
		Force   bool        `json:"force"`
	}{id, status, message, stage, force})
	if err != nil {
		return task.Task{}, false, err
	}
	return requested.UpdateRequested(ctx, actor, requestID, operation, id, eventForStatus(status), message, func(t *task.Task) bool {
		return s.applyStatusAndStage(t, status, message, stage)
	})
}

func (s *Service) setStatus(ctx context.Context, id string, status task.Status, message string, stage *string, force bool, workerID string) error {
	status, ok := task.ParseStatus(string(status))
	if !ok {
		return task.ErrInvalidStatus
	}
	if !force && isTerminalStatus(status) && strings.TrimSpace(message) == "" {
		return task.ErrMissingCompletionNote
	}
	event := eventForStatus(status)
	if workerID != "" {
		return s.store.UpdateFenced(ctx, id, workerID, event, message, func(t *task.Task) bool {
			return s.applyStatusAndStage(t, status, message, stage)
		})
	}
	return s.store.Update(ctx, id, event, message, func(t *task.Task) bool {
		return s.applyStatusAndStage(t, status, message, stage)
	})
}

func (s *Service) applyStatusAndStage(t *task.Task, status task.Status, message string, stage *string) bool {
	changed := t.SetStatus(status, s.now(), message)
	if stage != nil {
		t.Stage = *stage
		changed = true
	}
	return changed
}

func isTerminalStatus(status task.Status) bool {
	return status == task.StatusDone || status == task.StatusFailed
}

// Remove deletes a task. The public CLI uses deleted status instead.
func (s *Service) Remove(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// Prune physically removes tasks matching statuses. It is retained for internal
// callers; the public CLI uses deleted status instead.
func (s *Service) Prune(ctx context.Context, statuses []task.Status) (int, error) {
	for _, status := range statuses {
		if !task.ValidStatus(status) {
			return 0, task.ErrInvalidStatus
		}
	}
	return s.store.Prune(ctx, statuses)
}

// Take atomically claims the next ready task for workerID, optionally setting
// a lease. workerID empty falls back to a default identifier. This is the
// sole claim entry point — Pop / PopWithLease / PopWithLeaseForWorker were
// removed; all callers now use Take directly.
func (s *Service) Take(ctx context.Context, lease time.Duration, workerID, agent string) (*task.Task, error) {
	now := s.now()
	return s.store.ClaimNextForWorker(ctx, now, leaseExpires(now, lease), workerOrDefault(workerID), agent)
}

// TakeTask atomically claims one explicit ready task and optionally satisfies
// owner-approved gates in the same transaction.
func (s *Service) TakeTask(ctx context.Context, id string, lease time.Duration, workerID, agent string, satisfyGates []string) (*task.Task, error) {
	claimer, ok := s.store.(interface {
		ClaimTaskForWorker(context.Context, string, time.Time, time.Time, string, string, []string) (*task.Task, error)
	})
	if !ok {
		return nil, errors.New("explicit task claim is unsupported by this store")
	}
	now := s.now()
	return claimer.ClaimTaskForWorker(ctx, id, now, leaseExpires(now, lease), workerOrDefault(workerID), agent, satisfyGates)
}

// Heartbeat extends a worker-owned active claim lease.
func (s *Service) Heartbeat(ctx context.Context, taskID, workerID string, lease time.Duration) error {
	now := s.now()
	return s.store.Heartbeat(ctx, taskID, workerOrDefault(workerID), now, leaseExpires(now, lease))
}

// RequeueStale resets stale doing tasks to todo.
func (s *Service) RequeueStale(ctx context.Context, olderThan time.Duration) ([]task.Task, error) {
	return s.store.RequeueStale(ctx, olderThan, s.now())
}

// eventForStatus maps a validated task.Status to its lifecycle event via the
// task package's single status-metadata table. All callers MUST validate via
// task.ParseStatus first (setStatus does at service_lifecycle.go:42).
func eventForStatus(status task.Status) task.EventType {
	return task.EventForStatus(status)
}
