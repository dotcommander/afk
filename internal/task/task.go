// Package task defines the queue task schema and state transitions.
package task

import (
	"errors"
	"time"
)

// Status is the persisted task status. JSON encodes it as a string.
type Status string

// Status values for Task.Status.
const (
	StatusTodo    Status = "todo"
	StatusDoing   Status = "doing"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusDeleted Status = "deleted"

	StatusBudgetLimited Status = "budget-limited"
)

// ErrInvalidStatus reports an unknown task status.
var ErrInvalidStatus = errors.New("invalid task status")

// Task is the persisted task schema, also used for JSON CLI output.
type Task struct {
	ID           string       `json:"id"`
	Created      string       `json:"created"`
	Status       Status       `json:"status"`
	Body         string       `json:"body"`
	Priority     Priority     `json:"priority,omitzero"`
	Tags         []string     `json:"tags,omitempty"`
	CWD          string       `json:"cwd,omitzero"`
	Source       string       `json:"source,omitzero"`
	Agent        string       `json:"agent,omitzero"`
	GroupID      string       `json:"group_id,omitzero"`
	ResourceKey  string       `json:"resource_key,omitzero"`
	AvailableAt  string       `json:"available_at,omitzero"`
	Started      string       `json:"started,omitzero"`
	LeaseExpires string       `json:"lease_expires,omitzero"`
	Finished     string       `json:"finished,omitzero"`
	Error        string       `json:"error,omitzero"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
	Stage        string       `json:"stage,omitempty"` // free-form human pipeline state; empty = unset
}

// AddOptions carries metadata for a new task.
type AddOptions struct {
	Body        string
	Priority    Priority
	Tags        []string
	CWD         string
	Source      string
	Agent       string
	GroupID     string
	ResourceKey string
	AvailableAt string
	Stage       string
}

// AddOptionsFromTask returns the validation-relevant add metadata for an
// already persisted task.
func AddOptionsFromTask(t Task) AddOptions {
	return AddOptions{
		Body:        t.Body,
		Priority:    t.Priority,
		Tags:        append([]string(nil), t.Tags...),
		CWD:         t.CWD,
		Source:      t.Source,
		Agent:       t.Agent,
		GroupID:     t.GroupID,
		ResourceKey: t.ResourceKey,
		AvailableAt: t.AvailableAt,
	}
}

// RetryDisposition names the operator-selected outcome for a failed attempt.
// It is a bounded vocabulary only; AFK does not automatically retry tasks.
type RetryDisposition string

const (
	// RetryDispositionManual opens a new attempt immediately.
	RetryDispositionManual RetryDisposition = "manual"
	// RetryDispositionDeferred returns work to todo until available_at.
	RetryDispositionDeferred RetryDisposition = "deferred"
)

// ErrInvalidRetryDisposition reports an unsupported retry outcome.
var ErrInvalidRetryDisposition = errors.New("retry disposition must be manual or deferred")

// ErrDeferredRetryRequiresAvailableAt reports a deferred retry without a
// future eligibility timestamp.
var ErrDeferredRetryRequiresAvailableAt = errors.New("deferred retry requires available-at")

// ErrDeferredRetryNotFuture reports a deferred retry whose eligibility time
// has already arrived.
var ErrDeferredRetryNotFuture = errors.New("deferred retry available-at must be in the future")

// ErrManualRetryWithAvailableAt reports an ambiguous immediate retry request.
var ErrManualRetryWithAvailableAt = errors.New("manual retry does not accept available-at")

// ParseRetryDisposition returns a known retry disposition. Empty input uses
// the manual default so callers must opt in to deferred scheduling.
func ParseRetryDisposition(value string) (RetryDisposition, error) {
	if value == "" {
		return RetryDispositionManual, nil
	}
	switch RetryDisposition(value) {
	case RetryDispositionManual, RetryDispositionDeferred:
		return RetryDisposition(value), nil
	default:
		return "", ErrInvalidRetryDisposition
	}
}

// ValidateRetryDisposition validates disposition-specific scheduling input
// and returns a canonical UTC eligibility timestamp.
func ValidateRetryDisposition(disposition RetryDisposition, availableAt string) (string, error) {
	canonical, err := CanonicalAvailableAt(availableAt)
	if err != nil {
		return "", err
	}
	switch disposition {
	case RetryDispositionManual:
		if canonical != "" {
			return "", ErrManualRetryWithAvailableAt
		}
	case RetryDispositionDeferred:
		if canonical == "" {
			return "", ErrDeferredRetryRequiresAvailableAt
		}
	default:
		return "", ErrInvalidRetryDisposition
	}
	return canonical, nil
}

// EventType is a durable lifecycle event name.
type EventType string

// Event type values recorded in durable task history.
const (
	EventAdded           EventType = "added"
	EventClaimed         EventType = "claimed"
	EventDone            EventType = "done"
	EventFailed          EventType = "failed"
	EventDeleted         EventType = "deleted"
	EventRemoved         EventType = "removed"
	EventPruned          EventType = "pruned"
	EventRequeued        EventType = "requeued"
	EventHeartbeat       EventType = "heartbeat"
	EventDependencyAdded EventType = "dependency_added"
	EventRelationAdded   EventType = "relation_added"
	EventBudgetLimited   EventType = "budget_limited"
)

// Event records a task lifecycle transition.
type Event struct {
	ID      int64     `json:"id"`
	TaskID  string    `json:"task_id"`
	Type    EventType `json:"type"`
	At      string    `json:"at"`
	Message string    `json:"message,omitzero"`
}

// Attempt records one execution attempt for a task.
type Attempt struct {
	ID       int64  `json:"id"`
	TaskID   string `json:"task_id"`
	Started  string `json:"started"`
	Finished string `json:"finished,omitzero"`
	Status   Status `json:"status"`
	Error    string `json:"error,omitzero"`
	WorkerID string `json:"worker_id,omitzero"`
	Agent    string `json:"agent,omitzero"`
}

// Dependency records that TaskID is blocked by DependsOnID.
type Dependency struct {
	TaskID      string       `json:"task_id"`
	DependsOnID string       `json:"depends_on_id"`
	Type        RelationType `json:"type,omitempty"`
	Created     string       `json:"created"`
}

// ErrGateNotFound reports that a named gate does not exist for a task.
var ErrGateNotFound = errors.New("gate not found")

// Gate is a named boolean precondition on a task. SatisfiedAt is nil until satisfied.
type Gate struct {
	TaskID      string     `json:"task_id"`
	Name        string     `json:"name"`
	Satisfied   bool       `json:"satisfied"`
	CreatedAt   time.Time  `json:"created_at"`
	SatisfiedAt *time.Time `json:"satisfied_at,omitempty"`
}

// ValidStatus reports whether s is a known status.
func ValidStatus(s Status) bool {
	_, ok := ParseStatus(string(s))
	return ok
}

// ParseStatus returns the canonical status for user or persisted input.
func ParseStatus(s string) (Status, bool) {
	switch Status(s) {
	case StatusTodo, "pending":
		return StatusTodo, true
	case StatusDoing, "working":
		return StatusDoing, true
	case StatusDone:
		return StatusDone, true
	case StatusFailed:
		return StatusFailed, true
	case StatusDeleted:
		return StatusDeleted, true
	case StatusBudgetLimited:
		return StatusBudgetLimited, true
	default:
		return "", false
	}
}

// NormalizeStatus returns the canonical status, or s unchanged if unknown.
func NormalizeStatus(s Status) Status {
	normalized, ok := ParseStatus(string(s))
	if ok {
		return normalized
	}
	return s
}

// VisibleStatus reports whether s should appear in default task views.
func VisibleStatus(s Status) bool {
	return NormalizeStatus(s) != StatusDeleted
}

// ActiveStatus reports whether s represents unfinished work.
func ActiveStatus(s Status) bool {
	switch NormalizeStatus(s) {
	case StatusTodo, StatusDoing:
		return true
	default:
		return false
	}
}

// OrderedStatuses returns the canonical display order.
func OrderedStatuses() []Status {
	return []Status{StatusTodo, StatusDoing, StatusDone, StatusFailed, StatusDeleted, StatusBudgetLimited}
}

// AllStatuses returns every defined Status constant. It is an alias of
// OrderedStatuses (the canonical enumerator) so exhaustiveness checks and
// display logic read from one source.
func AllStatuses() []Status {
	return OrderedStatuses()
}

// MarkWorking claims the task for work and records the start timestamp.
func (t *Task) MarkWorking(now time.Time) {
	t.Status = StatusDoing
	t.Started = formatTime(now)
	t.LeaseExpires = ""
	t.Finished = ""
	t.Error = ""
}

// SetLease records when a working claim expires. Zero time clears the lease.
func (t *Task) SetLease(expires time.Time) {
	if expires.IsZero() {
		t.LeaseExpires = ""
		return
	}
	t.LeaseExpires = formatTime(expires)
}

// MarkDone marks the task complete. Already-done tasks are left unchanged.
func (t *Task) MarkDone(now time.Time) bool {
	if t.Status == StatusDone {
		return false
	}
	t.Status = StatusDone
	t.Finished = formatTime(now)
	t.LeaseExpires = ""
	t.Error = ""
	return true
}

// MarkFailed marks the task failed. Already-failed tasks are left unchanged.
func (t *Task) MarkFailed(now time.Time, reason string) bool {
	if t.Status == StatusFailed {
		return false
	}
	t.Status = StatusFailed
	t.Finished = formatTime(now)
	t.LeaseExpires = ""
	t.Error = reason
	return true
}

// MarkDeleted marks the task deleted without physically removing history.
func (t *Task) MarkDeleted(now time.Time, reason string) bool {
	if t.Status == StatusDeleted {
		return false
	}
	t.Status = StatusDeleted
	t.Finished = formatTime(now)
	t.LeaseExpires = ""
	t.Error = reason
	return true
}

// SetStatus applies a generic lifecycle transition via the statusMeta table.
func (t *Task) SetStatus(status Status, now time.Time, message string) bool {
	meta, ok := statusMeta[NormalizeStatus(status)]
	if !ok {
		return false
	}
	return meta.Apply(t, now, message)
}

// MarkBudgetLimited suspends the task pending user action when a goal budget
// cap is exhausted. Already-budget-limited tasks are left unchanged.
func (t *Task) MarkBudgetLimited(now time.Time, reason string) bool {
	if t.Status == StatusBudgetLimited {
		return false
	}
	t.Status = StatusBudgetLimited
	t.Finished = formatTime(now)
	t.LeaseExpires = ""
	t.Error = reason
	return true
}

// GoalGroup is the durable record of a compiled goal: its objective, current
// status, and the group id that links its member tasks. No I/O — the store
// owns persistence.
type GoalGroup struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
	// Outcome is the contract's restated outcome, kept for reference; Objective is the raw user objective.
	Outcome            string        `json:"outcome"`
	Status             string        `json:"status"`
	CreatedAt          string        `json:"created_at"`
	GroupID            string        `json:"group_id"`
	MaxTokens          int64         `json:"max_tokens"`
	MaxIterations      int64         `json:"max_iterations"`
	MaxDuration        time.Duration `json:"max_duration_ns"`
	TokenRegex         string        `json:"token_regex"`
	BudgetEpochStarted string        `json:"epoch_started"`
	TokensUsed         int64         `json:"tokens_used"`
	IterationsUsed     int64         `json:"iterations_used"`
	LimitReason        string        `json:"reason"`
	LimitedAt          string        `json:"limited_at"`
}

// Reset returns the task to todo and clears lifecycle/error fields.
func (t *Task) Reset() {
	t.Status = StatusTodo
	t.Started = ""
	t.LeaseExpires = ""
	t.Finished = ""
	t.Error = ""
}

func formatTime(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}
