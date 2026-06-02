package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/task"
	"github.com/google/uuid"
)

// recentPathLimit caps how many distinct working directories RecentPaths returns.
const recentPathLimit = 10

// Service coordinates task use cases across the store and task model.
type Service struct {
	store       Store
	now         func() time.Time
	newID       func() string // overridable for tests; defaults to uuid.NewString
	sidecarPath string        // empty disables rejection sidecar
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
	s := &Service{store: store, now: now, newID: uuid.NewString}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithIDGenerator overrides the task-ID source; intended for tests that need
// deterministic IDs. Production callers should accept the default
// uuid.NewString.
func WithIDGenerator(fn func() string) Option {
	return func(s *Service) { s.newID = fn }
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
// ID format: google/uuid v4 string (collision probability ~0; no List+retry
// loop needed). Existing on-disk numeric/seconds IDs remain valid since the
// id column is plain TEXT.
func (s *Service) addValidated(ctx context.Context, opts task.AddOptions) (string, error) {
	now := s.now()
	t := task.Task{
		ID:          s.newID(),
		Created:     formatTime(now),
		Status:      task.StatusTodo,
		Body:        opts.Body,
		Priority:    opts.Priority,
		Tags:        opts.Tags,
		CWD:         opts.CWD,
		Source:      opts.Source,
		Agent:       opts.Agent,
		GroupID:     opts.GroupID,
		ResourceKey: opts.ResourceKey,
		Stage:       opts.Stage,
	}
	if err := s.store.Add(ctx, t); err != nil {
		return "", err
	}
	return t.ID, nil
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

// Show returns one task by id. Uses an indexed Get rather than a full-table
// List+scan so single-task lookups stay O(1) on the primary key.
func (s *Service) Show(ctx context.Context, id string) (task.Task, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return task.Task{}, err
	}
	deps, err := s.store.Dependencies(ctx, id)
	if err != nil {
		return task.Task{}, err
	}
	t.Dependencies = deps
	return t, nil
}

// Count returns per-status tallies. Keys are raw (un-normalized) to match
// the prior behavior of bucketing by stored status value.
func (s *Service) Count(ctx context.Context) (map[task.Status]int, error) {
	return s.store.Counts(ctx)
}

// StatusSnapshot is a single queue snapshot: per-status tallies plus the
// todo and doing task lists. It collapses the count + tasks todo +
// tasks doing stitch into one read.
type StatusSnapshot struct {
	Counts map[task.Status]int `json:"counts"`
	Todo   []task.Task         `json:"todo"`
	Doing  []task.Task         `json:"doing"`
}

// Status returns a single queue snapshot: per-status tallies plus the todo
// and doing task lists. Aggregation runs in SQL (Counts + two indexed
// per-status queries via ActiveLists) instead of a full-table scan.
func (s *Service) Status(ctx context.Context) (StatusSnapshot, error) {
	raw, err := s.store.Counts(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	todo, doing, err := s.store.ActiveLists(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	// Fold legacy aliases ("pending" → todo, "working" → doing) into canonical
	// buckets so the snapshot key shape matches the pre-SQL behavior.
	counts := make(map[task.Status]int, len(raw))
	for status, n := range raw {
		counts[task.NormalizeStatus(status)] += n
	}
	return StatusSnapshot{Counts: counts, Todo: todo, Doing: doing}, nil
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

// AddRelation records a typed edge from taskID to relatedID.
func (s *Service) AddRelation(ctx context.Context, taskID, relatedID string, relType task.RelationType) error {
	return s.store.AddRelation(ctx, taskID, relatedID, relType)
}

// Dependencies returns the tasks that taskID is blocked by.
func (s *Service) Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error) {
	return s.store.Dependencies(ctx, taskID)
}

// AddGate records a named boolean precondition on a task.
func (s *Service) AddGate(ctx context.Context, taskID, name string) error {
	return s.store.AddGate(ctx, taskID, name)
}

// SatisfyGate marks a gate satisfied.
func (s *Service) SatisfyGate(ctx context.Context, taskID, name string) error {
	return s.store.SatisfyGate(ctx, taskID, name)
}

// Gates returns all gates for a task.
func (s *Service) Gates(ctx context.Context, taskID string) ([]task.Gate, error) {
	return s.store.Gates(ctx, taskID)
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
