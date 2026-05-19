package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// Service coordinates task use cases across the store and task model.
type Service struct {
	store store.Store
	now   func() time.Time
}

// ExplainData contains task state plus durable history.
type ExplainData struct {
	Task     task.Task      `json:"task"`
	Events   []task.Event   `json:"events"`
	Attempts []task.Attempt `json:"attempts"`
}

// NewService constructs a Service.
func NewService(store store.Store, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

// Add appends a new pending task and returns its id.
func (s *Service) Add(ctx context.Context, body string) (string, error) {
	return s.AddWithOptions(ctx, task.AddOptions{Body: body})
}

// AddWithOptions appends a new pending task with metadata and returns its id.
func (s *Service) AddWithOptions(ctx context.Context, opts task.AddOptions) (string, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return "", err
	}
	base := strconv.FormatInt(s.now().UTC().Unix(), 10)
	t := task.Task{
		ID:      uniqueID(tasks, base),
		Created: formatTime(s.now()),
		Status:  task.StatusPending,
		Body:    opts.Body,

		Priority:    opts.Priority,
		Tags:        opts.Tags,
		CWD:         opts.CWD,
		Source:      opts.Source,
		Agent:       opts.Agent,
		GroupID:     opts.GroupID,
		ResourceKey: opts.ResourceKey,
	}
	if err := s.store.Add(ctx, t); err != nil {
		return "", err
	}
	return t.ID, nil
}

// List returns tasks filtered by status, or all tasks if statusFilter is empty.
func (s *Service) List(ctx context.Context, statusFilter string) ([]task.Task, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	return filterByStatus(tasks, statusFilter), nil
}

// Show returns one task by id.
func (s *Service) Show(ctx context.Context, id string) (task.Task, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return task.Task{}, err
	}
	idx, ok := findTask(tasks, id)
	if !ok {
		return task.Task{}, fmt.Errorf("show %s: %w", id, ErrNotFound)
	}
	return tasks[idx], nil
}

// Count returns per-status tallies.
func (s *Service) Count(ctx context.Context) (map[task.Status]int, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	tally := make(map[task.Status]int)
	for _, t := range tasks {
		tally[t.Status]++
	}
	return tally, nil
}

// Next returns the first pending task without mutation.
func (s *Service) Next(ctx context.Context) (*task.Task, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.Status == task.StatusPending {
			next := t
			return &next, nil
		}
	}
	return nil, nil
}

// Edit replaces a task body.
func (s *Service) Edit(ctx context.Context, id, body string) error {
	return s.store.Update(ctx, id, task.EventEdited, "", func(t *task.Task) bool {
		t.Body = body
		return true
	})
}

// Done marks a task done.
func (s *Service) Done(ctx context.Context, id string) error {
	return s.store.Update(ctx, id, task.EventDone, "", func(t *task.Task) bool {
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

// Remove deletes a task.
func (s *Service) Remove(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// Prune removes tasks matching statuses.
func (s *Service) Prune(ctx context.Context, statuses []task.Status) error {
	return s.store.Prune(ctx, statuses)
}

// Pop atomically claims the next pending task.
func (s *Service) Pop(ctx context.Context) (*task.Task, error) {
	return s.PopWithLease(ctx, 0)
}

// PopWithLease atomically claims the next pending task, optionally setting a lease.
func (s *Service) PopWithLease(ctx context.Context, lease time.Duration) (*task.Task, error) {
	var expires time.Time
	if lease > 0 {
		expires = s.now().Add(lease)
	}
	return s.store.ClaimNext(ctx, s.now(), expires)
}

// Explain returns task state and lifecycle history.
func (s *Service) Explain(ctx context.Context, id string) (ExplainData, error) {
	t, err := s.Show(ctx, id)
	if err != nil {
		return ExplainData{}, err
	}
	events, err := s.store.Events(ctx, id)
	if err != nil {
		return ExplainData{}, err
	}
	attempts, err := s.store.Attempts(ctx, id)
	if err != nil {
		return ExplainData{}, err
	}
	return ExplainData{Task: t, Events: events, Attempts: attempts}, nil
}

// Retry resets a failed task to pending while preserving attempt history.
func (s *Service) Retry(ctx context.Context, id string) error {
	return s.store.Update(ctx, id, task.EventRetried, "", func(t *task.Task) bool {
		if t.Status != task.StatusFailed {
			return false
		}
		t.Reset()
		return true
	})
}

// RequeueStale resets stale working tasks to pending.
func (s *Service) RequeueStale(ctx context.Context, olderThan time.Duration) ([]task.Task, error) {
	return s.store.RequeueStale(ctx, olderThan, s.now())
}

func uniqueID(tasks []task.Task, base string) string {
	candidate := base
	for i := 1; ; i++ {
		if _, found := findTask(tasks, candidate); !found {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func findTask(tasks []task.Task, id string) (int, bool) {
	for i := range tasks {
		if tasks[i].ID == id {
			return i, true
		}
	}
	return -1, false
}

func filterByStatus(tasks []task.Task, status string) []task.Task {
	if status == "" {
		return tasks
	}
	out := tasks[:0:0]
	for _, t := range tasks {
		if string(t.Status) == status {
			out = append(out, t)
		}
	}
	return out
}

func formatTime(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}
