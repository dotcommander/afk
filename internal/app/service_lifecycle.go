package app

import (
	"context"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// This file holds the Service methods that transition a task's lifecycle
// state or manage worker claims: terminal transitions (Done, Fail, Reset,
// Retry), removal/pruning, priority promotion, the Pop claim family, lease
// heartbeats, and stale-claim requeue. Read-only and dependency/readiness
// methods live in service.go.

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

// Reset returns a task to pending.
func (s *Service) Reset(ctx context.Context, id string) error {
	return s.store.Update(ctx, id, task.EventReset, "", func(t *task.Task) bool {
		t.Reset()
		return true
	})
}

// Retry resets a failed task to pending while preserving attempt history.
func (s *Service) Retry(ctx context.Context, id string) error {
	tk, err := s.Show(ctx, id)
	if err != nil {
		return err
	}
	if tk.Status == task.StatusFailed {
		if err := task.ValidateBody(tk.Body); err != nil {
			return err
		}
	}
	return s.store.Update(ctx, id, task.EventRetried, "", func(t *task.Task) bool {
		if t.Status != task.StatusFailed {
			return false
		}
		t.Reset()
		return true
	})
}

// Remove deletes a task.
func (s *Service) Remove(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// Prune removes tasks matching statuses.
func (s *Service) Prune(ctx context.Context, statuses []task.Status) error {
	return s.store.Prune(ctx, statuses)
}

// PruneByTag deletes tasks tagged with tag. Returns count deleted.
func (s *Service) PruneByTag(ctx context.Context, tag string) (int, error) {
	return s.store.PruneByTag(ctx, tag)
}

// Promote moves a pending task ahead of peers with the same effective priority.
func (s *Service) Promote(ctx context.Context, id string) error {
	return s.store.Promote(ctx, id)
}

// Pop atomically claims the next pending task.
func (s *Service) Pop(ctx context.Context) (*task.Task, error) {
	return s.PopWithLease(ctx, 0)
}

// PopWithLease atomically claims the next pending task, optionally setting a lease.
func (s *Service) PopWithLease(ctx context.Context, lease time.Duration) (*task.Task, error) {
	return s.PopWithLeaseForWorker(ctx, lease, "", "")
}

// PopWithLeaseForWorker atomically claims the next ready task for workerID.
func (s *Service) PopWithLeaseForWorker(ctx context.Context, lease time.Duration, workerID, agent string) (*task.Task, error) {
	now := s.now()
	return s.store.ClaimNextForWorker(ctx, now, leaseExpires(now, lease), workerOrDefault(workerID), agent)
}

// Heartbeat extends a worker-owned active claim lease.
func (s *Service) Heartbeat(ctx context.Context, taskID, workerID string, lease time.Duration) error {
	now := s.now()
	return s.store.Heartbeat(ctx, taskID, workerOrDefault(workerID), now, leaseExpires(now, lease))
}

// RequeueStale resets stale working tasks to pending.
func (s *Service) RequeueStale(ctx context.Context, olderThan time.Duration) ([]task.Task, error) {
	return s.store.RequeueStale(ctx, olderThan, s.now())
}
