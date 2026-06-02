package app

import (
	"context"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// This file holds Service methods that transition task lifecycle state or
// manage worker claims. Some method names are legacy internal API names kept
// for existing callers; the public CLI now exposes these through `afk set` and
// `afk take`.

// Done marks a task done with an optional completion note.
func (s *Service) Done(ctx context.Context, id, note string) error {
	return s.store.Update(ctx, id, task.EventDone, note, func(t *task.Task) bool {
		return t.MarkDone(s.now())
	})
}

// Fail marks a task failed with reason.
func (s *Service) Fail(ctx context.Context, id, reason string) error {
	return s.store.Update(ctx, id, task.EventFailed, reason, func(t *task.Task) bool {
		return t.MarkFailed(s.now(), reason)
	})
}

// SetStatus moves a task to status and records message as lifecycle context.
func (s *Service) SetStatus(ctx context.Context, id string, status task.Status, message string) error {
	return s.setStatus(ctx, id, status, message, nil)
}

// SetStatusWithStage moves a task to status and, when stage is non-nil, also
// updates the free-form pipeline stage in the same atomic Update. A nil stage
// leaves the existing stage unchanged.
func (s *Service) SetStatusWithStage(ctx context.Context, id string, status task.Status, message string, stage *string) error {
	return s.setStatus(ctx, id, status, message, stage)
}

func (s *Service) setStatus(ctx context.Context, id string, status task.Status, message string, stage *string) error {
	status, ok := task.ParseStatus(string(status))
	if !ok {
		return task.ErrInvalidStatus
	}
	event := eventForStatus(status)
	return s.store.Update(ctx, id, event, message, func(t *task.Task) bool {
		changed := t.SetStatus(status, s.now(), message)
		if stage != nil {
			t.Stage = *stage
			changed = true
		}
		return changed
	})
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

// Heartbeat extends a worker-owned active claim lease.
func (s *Service) Heartbeat(ctx context.Context, taskID, workerID string, lease time.Duration) error {
	now := s.now()
	return s.store.Heartbeat(ctx, taskID, workerOrDefault(workerID), now, leaseExpires(now, lease))
}

// RequeueStale resets stale doing tasks to todo.
func (s *Service) RequeueStale(ctx context.Context, olderThan time.Duration) ([]task.Task, error) {
	return s.store.RequeueStale(ctx, olderThan, s.now())
}

// eventForStatus maps a validated task.Status to its lifecycle event. All
// callers MUST validate via task.ParseStatus first (SetStatus does at
// service_lifecycle.go:31). Unknown statuses panic — that signals a Status
// constant was added without updating this switch, not a runtime input bug.
func eventForStatus(status task.Status) task.EventType {
	switch status {
	case task.StatusDone:
		return task.EventDone
	case task.StatusFailed:
		return task.EventFailed
	case task.StatusDeleted:
		return task.EventDeleted
	case task.StatusDoing:
		return task.EventClaimed
	case task.StatusTodo:
		return task.EventRequeued
	default:
		panic("app: eventForStatus called with unknown status " + string(status) + " — callers must validate via task.ParseStatus first")
	}
}
