package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/google/uuid"
)

// recentPathLimit caps how many distinct working directories RecentPaths returns.
const recentPathLimit = 10

const queueHealthWindow = 24 * time.Hour

// statusFilterAll is the List/Find status filter that spans every status.
const statusFilterAll = "all"

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
	Gates    []task.Gate    `json:"gates,omitempty"`
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
		s.recordRejection(opts, err)
		return "", err
	}
	return s.addValidated(ctx, opts)
}

// AddWithOptionsBlockedBy appends a new todo task and records a blocking
// dependency as one queue operation. SQLite stores do this atomically; other
// stores get a rollback fallback so dependency-write failures do not leave
// newly inserted tasks claimable.
func (s *Service) AddWithOptionsBlockedBy(ctx context.Context, opts task.AddOptions, dependsOnID string) (string, error) {
	if dependsOnID == "" {
		return s.AddWithOptions(ctx, opts)
	}
	if err := task.ValidateAddOptions(opts); err != nil {
		s.recordRejection(opts, err)
		return "", err
	}
	return s.addValidatedWithDependency(ctx, opts, dependsOnID)
}

// AddWithRequest appends a task exactly once for an actor/request pair.
func (s *Service) AddWithRequest(ctx context.Context, actor, requestID string, opts task.AddOptions, dependsOnID string) (string, bool, error) {
	hasRequestID := requestID != ""
	actor = strings.TrimSpace(actor)
	requestID = strings.TrimSpace(requestID)
	if hasRequestID && requestID == "" {
		return "", false, errors.New("request id must not be blank")
	}
	if requestID == "" {
		id, err := s.AddWithOptionsBlockedBy(ctx, opts, dependsOnID)
		return id, false, err
	}
	if err := task.ValidateAddOptions(opts); err != nil {
		s.recordRejection(opts, err)
		return "", false, err
	}
	requested, ok := s.store.(interface {
		AddRequested(context.Context, string, string, string, task.Task, string) (string, bool, error)
	})
	if !ok {
		return "", false, errors.New("request id is unsupported by this store")
	}
	operation, err := requestOperation("task.add", struct {
		Options     task.AddOptions `json:"options"`
		DependsOnID string          `json:"depends_on_id,omitempty"`
	}{opts, dependsOnID})
	if err != nil {
		return "", false, err
	}
	return requested.AddRequested(ctx, actor, requestID, operation, s.newTask(opts), dependsOnID)
}

func requestOperation(name string, payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode request operation: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return name + ":" + hex.EncodeToString(sum[:]), nil
}

// ImportVybe imports a validated Vybe archive through stores that support the
// retirement migration contract.
func (s *Service) ImportVybe(ctx context.Context, source string, apply bool) (store.VybeImportReport, error) {
	importer, ok := s.store.(interface {
		ImportVybeArchive(context.Context, store.VybeImportOptions) (store.VybeImportReport, error)
	})
	if !ok {
		return store.VybeImportReport{}, errors.New("vybe import is unsupported by this store")
	}
	return importer.ImportVybeArchive(ctx, store.VybeImportOptions{Source: source, Apply: apply})
}

// AddCheckpoint writes one task-scoped durable progress record.
func (s *Service) AddCheckpoint(ctx context.Context, c task.Checkpoint) (task.Checkpoint, error) {
	writer, ok := s.store.(interface {
		AddCheckpoint(context.Context, task.Checkpoint) (task.Checkpoint, error)
	})
	if !ok {
		return task.Checkpoint{}, errors.New("checkpoints are unsupported by this store")
	}
	if c.CreatedAt == "" {
		c.CreatedAt = formatTime(s.now())
	}
	if c.Provenance.System == "" {
		c.Provenance.System = "afk-cli"
	}
	if c.Provenance.ID == "" {
		c.Provenance.ID = uuid.NewString()
	}
	return writer.AddCheckpoint(ctx, c)
}

// Checkpoints returns durable progress records in insertion order.
func (s *Service) Checkpoints(ctx context.Context, taskID string) ([]task.Checkpoint, error) {
	reader, ok := s.store.(interface {
		Checkpoints(context.Context, string) ([]task.Checkpoint, error)
	})
	if !ok {
		return nil, errors.New("checkpoints are unsupported by this store")
	}
	return reader.Checkpoints(ctx, taskID)
}

// AddArtifact writes one task-owned artifact record.
func (s *Service) AddArtifact(ctx context.Context, a task.Artifact) error {
	writer, ok := s.store.(interface {
		AddArtifact(context.Context, task.Artifact) error
	})
	if !ok {
		return errors.New("artifacts are unsupported by this store")
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt == "" {
		a.CreatedAt = formatTime(s.now())
	}
	if a.MetadataJSON == "" {
		a.MetadataJSON = "{}"
	}
	if a.Provenance.System == "" {
		a.Provenance.System = "afk-cli"
	}
	if a.Provenance.ID == "" {
		a.Provenance.ID = a.ID
	}
	return writer.AddArtifact(ctx, a)
}

// Artifacts returns task artifacts in source order.
func (s *Service) Artifacts(ctx context.Context, taskID string) ([]task.Artifact, error) {
	reader, ok := s.store.(interface {
		Artifacts(context.Context, string) ([]task.Artifact, error)
	})
	if !ok {
		return nil, errors.New("artifacts are unsupported by this store")
	}
	return reader.Artifacts(ctx, taskID)
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
	s.recordRejection(opts, err)
	// Tag the task so downstream consumers can see it was force-added.
	opts.Tags = append(append([]string{}, opts.Tags...), "force-added")
	return s.addValidated(ctx, opts)
}

// AddWithOptionsForceBlockedBy is the force-add variant of AddWithOptionsBlockedBy.
func (s *Service) AddWithOptionsForceBlockedBy(ctx context.Context, opts task.AddOptions, dependsOnID string) (string, error) {
	if dependsOnID == "" {
		return s.AddWithOptionsForce(ctx, opts)
	}
	err := task.ValidateAddOptions(opts)
	if err == nil {
		return s.AddWithOptionsBlockedBy(ctx, opts, dependsOnID)
	}
	if !errors.Is(err, task.ErrInvalidTask) {
		return "", err
	}
	s.recordRejection(opts, err)
	opts.Tags = append(append([]string{}, opts.Tags...), "force-added")
	return s.addValidatedWithDependency(ctx, opts, dependsOnID)
}

// recordRejection persists invalid add attempts when a sidecar is configured.
// Sidecar write failures are intentionally non-fatal: the validation error is
// the contract, and masking it would be worse than losing one sidecar line.
func (s *Service) recordRejection(opts task.AddOptions, reason error) {
	if s.sidecarPath == "" {
		return
	}
	_ = RecordRejection(s.sidecarPath, opts, reason, s.now())
}

// addValidated inserts a task that has already passed (or been exempted from)
// validation. It is the single source of truth for "how opts becomes a row".
// ID format: google/uuid v4 string (collision probability ~0; no List+retry
// loop needed). Existing on-disk numeric/seconds IDs remain valid since the
// id column is plain TEXT.
func (s *Service) addValidated(ctx context.Context, opts task.AddOptions) (string, error) {
	t := s.newTask(opts)
	if err := s.store.Add(ctx, t); err != nil {
		return "", err
	}
	return t.ID, nil
}

func (s *Service) addValidatedWithDependency(ctx context.Context, opts task.AddOptions, dependsOnID string) (string, error) {
	t := s.newTask(opts)
	if atomicStore, ok := s.store.(interface {
		AddWithDependency(context.Context, task.Task, string) error
	}); ok {
		if err := atomicStore.AddWithDependency(ctx, t, dependsOnID); err != nil {
			return "", err
		}
		return t.ID, nil
	}
	if err := s.store.Add(ctx, t); err != nil {
		return "", err
	}
	if err := s.store.AddDependency(ctx, t.ID, dependsOnID); err != nil {
		_ = s.store.Delete(ctx, t.ID)
		return "", err
	}
	return t.ID, nil
}

func (s *Service) newTask(opts task.AddOptions) task.Task {
	now := s.now()
	// Every path to newTask validates AddOptions first, including force-add,
	// which only bypasses ErrInvalidTask body checks.
	availableAt, _ := task.CanonicalAvailableAt(opts.AvailableAt)
	return task.Task{
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
		AvailableAt: availableAt,
		Stage:       opts.Stage,
	}
}

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
