package app

import (
	"context"
	"fmt"
	"os"
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

// NotReadyReason explains why a task is not ready to be claimed.
type NotReadyReason struct {
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// ReadinessData contains a task and its computed readiness reasons.
type ReadinessData struct {
	Task    task.Task        `json:"task"`
	Ready   bool             `json:"ready"`
	Reasons []NotReadyReason `json:"not_ready_reasons,omitempty"`
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
	tasks, err := s.store.Ready(ctx, store.ReadyOptions{Now: s.now()})
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	next := tasks[0]
	return &next, nil
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

// PruneByTag deletes tasks tagged with tag. Returns count deleted.
func (s *Service) PruneByTag(ctx context.Context, tag string) (int, error) {
	return s.store.PruneByTag(ctx, tag)
}

// Pop atomically claims the next pending task.
func (s *Service) Pop(ctx context.Context) (*task.Task, error) {
	return s.PopWithLease(ctx, 0)
}

// PopWithLease atomically claims the next pending task, optionally setting a lease.
func (s *Service) PopWithLease(ctx context.Context, lease time.Duration) (*task.Task, error) {
	return s.PopWithLeaseForWorker(ctx, lease, defaultWorkerID(), "")
}

// PopWithLeaseForWorker atomically claims the next ready task for workerID.
func (s *Service) PopWithLeaseForWorker(ctx context.Context, lease time.Duration, workerID, agent string) (*task.Task, error) {
	if workerID == "" {
		workerID = defaultWorkerID()
	}
	var expires time.Time
	if lease > 0 {
		expires = s.now().Add(lease)
	}
	return s.store.ClaimNextForWorker(ctx, s.now(), expires, workerID, agent)
}

// Heartbeat extends a worker-owned active claim lease.
func (s *Service) Heartbeat(ctx context.Context, taskID, workerID string, lease time.Duration) error {
	if workerID == "" {
		workerID = defaultWorkerID()
	}
	var expires time.Time
	if lease > 0 {
		expires = s.now().Add(lease)
	}
	return s.store.Heartbeat(ctx, taskID, workerID, s.now(), expires)
}

// AddDependency records that taskID is blocked by dependsOnID.
func (s *Service) AddDependency(ctx context.Context, taskID, dependsOnID string) error {
	return s.store.AddDependency(ctx, taskID, dependsOnID)
}

// RemoveDependency removes a blocked-by edge.
func (s *Service) RemoveDependency(ctx context.Context, taskID, dependsOnID string) error {
	return s.store.RemoveDependency(ctx, taskID, dependsOnID)
}

// Dependencies returns the tasks that taskID is blocked by.
func (s *Service) Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error) {
	return s.store.Dependencies(ctx, taskID)
}

// Block records a manual scheduling block.
func (s *Service) Block(ctx context.Context, taskID, reason string) error {
	return s.store.Block(ctx, taskID, reason)
}

// Unblock removes a manual scheduling block.
func (s *Service) Unblock(ctx context.Context, taskID string) error {
	return s.store.Unblock(ctx, taskID)
}

// Ready returns pending tasks with no unfinished dependencies.
func (s *Service) Ready(ctx context.Context) ([]task.Task, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := tasksByID(tasks)
	ready := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Status != task.StatusPending {
			continue
		}
		reasons, err := s.notReadyReasons(ctx, t.ID, byID)
		if err != nil {
			return nil, err
		}
		if len(reasons) == 0 {
			ready = append(ready, t)
		}
	}
	return ready, nil
}

// Why returns computed readiness information for one task.
func (s *Service) Why(ctx context.Context, id string) (ReadinessData, error) {
	t, err := s.Show(ctx, id)
	if err != nil {
		return ReadinessData{}, err
	}
	tasks, err := s.store.List(ctx)
	if err != nil {
		return ReadinessData{}, err
	}
	reasons, err := s.notReadyReasons(ctx, id, tasksByID(tasks))
	if err != nil {
		return ReadinessData{}, err
	}
	return ReadinessData{Task: t, Ready: t.Status == task.StatusPending && len(reasons) == 0, Reasons: reasons}, nil
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

func tasksByID(tasks []task.Task) map[string]task.Task {
	byID := make(map[string]task.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	return byID
}

func (s *Service) notReadyReasons(ctx context.Context, id string, tasks map[string]task.Task) ([]NotReadyReason, error) {
	t, ok := tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	block, err := s.store.BlockForTask(ctx, id)
	if err != nil {
		return nil, err
	}
	var reasons []NotReadyReason
	if block != nil {
		reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "manual_block", Detail: block.Reason})
	}
	if t.ResourceKey != "" {
		nowText := formatTime(s.now())
		for _, active := range tasks {
			if active.ID == id || active.Status != task.StatusWorking || active.ResourceKey != t.ResourceKey {
				continue
			}
			if active.LeaseExpires == "" || active.LeaseExpires > nowText {
				reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "resource_locked", Detail: active.ID})
				break
			}
		}
	}
	deps, err := s.store.Dependencies(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, dep := range deps {
		depTask, ok := tasks[dep.DependsOnID]
		if !ok {
			reasons = append(reasons, NotReadyReason{
				TaskID: id,
				Kind:   "dependency_missing",
				Detail: dep.DependsOnID,
			})
			continue
		}
		switch depTask.Status {
		case task.StatusDone:
			continue
		case task.StatusPending:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_pending", Detail: dep.DependsOnID})
		case task.StatusWorking:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_working", Detail: dep.DependsOnID})
		case task.StatusFailed:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_failed", Detail: dep.DependsOnID})
		default:
			reasons = append(reasons, NotReadyReason{TaskID: id, Kind: "dependency_not_done", Detail: dep.DependsOnID})
		}
	}
	return reasons, nil
}

func formatTime(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}
