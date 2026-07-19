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
