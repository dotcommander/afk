package app

import (
	"context"
	"strings"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// List returns tasks filtered by status, or visible tasks if statusFilter is empty.
//
// A concrete status filter is pushed into a status-scoped SQL query
// (Store.ListByStatus over the status index) instead of a full-table scan.
// The "" (visible) and "all" cases still read the whole table and filter in
// Go via filterByStatus, since they span every status. Both paths preserve
// List() order (ordinal, rowid).
func (s *Service) List(ctx context.Context, statusFilter string) ([]task.Task, error) {
	if err := validateStatusFilter(statusFilter); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(statusFilter)
	if trimmed != "" && trimmed != statusFilterAll {
		// validateStatusFilter already guaranteed this parses.
		parsed, _ := task.ParseStatus(trimmed)
		return s.store.ListByStatus(ctx, parsed)
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
// directories — the most recently created ones, sorted alphabetically. The
// distinct-and-recent selection is pushed into SQL (Store.RecentDistinctCWDs)
// so no full-table sort happens in Go.
func (s *Service) RecentPaths(ctx context.Context) ([]string, error) {
	return s.store.RecentDistinctCWDs(ctx, recentPathLimit)
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
	Health task.QueueHealth    `json:"health"`
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
	health, err := s.store.QueueHealth(ctx, s.now(), queueHealthWindow)
	if err != nil {
		return StatusSnapshot{}, err
	}
	// Fold legacy aliases ("pending" → todo, "working" → doing) into canonical
	// buckets so the snapshot key shape matches the pre-SQL behavior.
	counts := make(map[task.Status]int, len(raw))
	for status, n := range raw {
		counts[task.NormalizeStatus(status)] += n
	}
	return StatusSnapshot{Counts: counts, Todo: todo, Doing: doing, Health: health}, nil
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

// ExplainNotReady returns a structured, formatting-free breakdown of why the
// queue has no claimable task. Readiness logic lives entirely in the store
// (readyWhereSQL); this is a pass-through.
func (s *Service) ExplainNotReady(ctx context.Context) (store.NotReadyExplanation, error) {
	return s.store.ExplainNotReady(ctx)
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
	gates, err := s.store.Gates(ctx, id)
	if err != nil {
		return ExplainData{}, err
	}
	return ExplainData{Task: t, Events: events, Attempts: attempts, Gates: gates}, nil
}
