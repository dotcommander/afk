package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// recentPathLimit caps how many distinct working directories RecentPaths returns.
const recentPathLimit = 10

// Service coordinates task use cases across the store and task model.
type Service struct {
	store       Store
	now         func() time.Time
	sidecarPath string // empty disables rejection sidecar
}

// Option configures a Service at construction time.
type Option func(*Service)

// WithSidecarPath enables persistence of rejected tasks to path.
func WithSidecarPath(path string) Option {
	return func(s *Service) { s.sidecarPath = path }
}

// ExplainData contains task state plus durable history.
type ExplainData struct {
	Task     task.Task      `json:"task"`
	Events   []task.Event   `json:"events"`
	Attempts []task.Attempt `json:"attempts"`
}

// NewService constructs a Service.
func NewService(store Store, now func() time.Time, opts ...Option) *Service {
	s := &Service{store: store, now: now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Add appends a new todo task and returns its id.
func (s *Service) Add(ctx context.Context, body string) (string, error) {
	return s.AddWithOptions(ctx, task.AddOptions{Body: body})
}

// AddWithOptions appends a new todo task with metadata and returns its id.
func (s *Service) AddWithOptions(ctx context.Context, opts task.AddOptions) (string, error) {
	if err := task.ValidateAddOptions(opts); err != nil {
		// Persist rejection so discovery work is not lost. Swallow sidecar
		// errors: the validation error is the contract; masking it would
		// be worse than losing one sidecar line.
		if s.sidecarPath != "" {
			_ = RecordRejection(s.sidecarPath, opts, err, s.now())
		}
		return "", err
	}
	return s.addValidated(ctx, opts)
}

// AddWithOptionsForce inserts a task even if validation rejects it. The
// rejection (if any) is still recorded to the sidecar for audit. When
// validation passes, behavior is identical to AddWithOptions — no
// force-added tag is appended and no sidecar record is written.
//
// Callers MUST gate this method on operator opt-in (e.g., env var). The
// service does NOT enforce that gate — that is a CLI concern.
func (s *Service) AddWithOptionsForce(ctx context.Context, opts task.AddOptions) (string, error) {
	err := task.ValidateAddOptions(opts)
	if err == nil {
		return s.AddWithOptions(ctx, opts)
	}
	if !errors.Is(err, task.ErrInvalidTask) {
		return "", err
	}
	// Record the rejection for audit, then proceed.
	if s.sidecarPath != "" {
		_ = RecordRejection(s.sidecarPath, opts, err, s.now())
	}
	// Tag the task so downstream consumers can see it was force-added.
	opts.Tags = append(append([]string{}, opts.Tags...), "force-added")
	return s.addValidated(ctx, opts)
}

// addValidated inserts a task that has already passed (or been exempted from)
// validation. It is the single source of truth for "how opts becomes a row".
func (s *Service) addValidated(ctx context.Context, opts task.AddOptions) (string, error) {
	now := s.now()
	base := strconv.FormatInt(now.UTC().Unix(), 10)
	created := formatTime(now)
	for range 16 {
		tasks, err := s.store.List(ctx)
		if err != nil {
			return "", err
		}
		t := task.Task{
			ID:      uniqueID(tasks, base),
			Created: created,
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
			if errors.Is(err, store.ErrDuplicateTask) {
				continue
			}
			return "", err
		}
		return t.ID, nil
	}
	return "", fmt.Errorf("add task: %w", store.ErrDuplicateTask)
}

// List returns tasks filtered by status, or visible tasks if statusFilter is empty.
func (s *Service) List(ctx context.Context, statusFilter string) ([]task.Task, error) {
	if err := validateStatusFilter(statusFilter); err != nil {
		return nil, err
	}
	tasks, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	return filterByStatus(tasks, statusFilter), nil
}

// Find returns visible tasks matching query across common task metadata fields.
func (s *Service) Find(ctx context.Context, query, statusFilter string) ([]task.Task, error) {
	if err := validateStatusFilter(statusFilter); err != nil {
		return nil, err
	}
	tasks, err := s.List(ctx, statusFilter)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return tasks, nil
	}
	var out []task.Task
	for _, t := range tasks {
		if taskMatches(t, query) {
			out = append(out, t)
		}
	}
	return out, nil
}

// RecentPaths returns up to recentPathLimit distinct non-empty task working
// directories — the most recently created ones, sorted alphabetically.
func (s *Service) RecentPaths(ctx context.Context) ([]string, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	return recentPaths(tasks, recentPathLimit), nil
}

// Show returns one task by id.
func (s *Service) Show(ctx context.Context, id string) (task.Task, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return task.Task{}, err
	}
	idx, ok := findTask(tasks, id)
	if !ok {
		return task.Task{}, fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	t := tasks[idx]
	deps, err := s.store.Dependencies(ctx, id)
	if err != nil {
		return task.Task{}, err
	}
	t.Dependencies = deps
	return t, nil
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

// StatusSnapshot is a single queue snapshot: per-status tallies plus the
// todo and doing task lists. It collapses the count + tasks todo +
// tasks doing stitch into one read.
type StatusSnapshot struct {
	Counts map[task.Status]int `json:"counts"`
	Todo   []task.Task         `json:"todo"`
	Doing  []task.Task         `json:"doing"`
}

// Status returns a single queue snapshot in one store read: per-status tallies
// plus the todo and doing task lists.
func (s *Service) Status(ctx context.Context) (StatusSnapshot, error) {
	tasks, err := s.store.List(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	snapshot := StatusSnapshot{Counts: make(map[task.Status]int)}
	for _, t := range tasks {
		status := task.NormalizeStatus(t.Status)
		snapshot.Counts[status]++
		switch status {
		case task.StatusPending:
			snapshot.Todo = append(snapshot.Todo, t)
		case task.StatusWorking:
			snapshot.Doing = append(snapshot.Doing, t)
		}
	}
	return snapshot, nil
}

// Next returns the first ready task without mutation.
func (s *Service) Next(ctx context.Context) (*task.Task, error) {
	tasks, err := s.store.Ready(ctx)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	next := tasks[0]
	return &next, nil
}

// AddDependency records that taskID is blocked by dependsOnID.
func (s *Service) AddDependency(ctx context.Context, taskID, dependsOnID string) error {
	return s.store.AddDependency(ctx, taskID, dependsOnID)
}

// Dependencies returns the tasks that taskID is blocked by.
func (s *Service) Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error) {
	return s.store.Dependencies(ctx, taskID)
}

// Ready returns todo tasks with no unfinished dependencies in scheduler order.
func (s *Service) Ready(ctx context.Context) ([]task.Task, error) {
	return s.store.Ready(ctx)
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
