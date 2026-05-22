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
	StatusPending Status = "todo"
	StatusWorking Status = "doing"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusDeleted Status = "deleted"
)

// ErrInvalidStatus reports an unknown task status.
var ErrInvalidStatus = errors.New("invalid task status")

// Task is the persisted task schema, also used for JSON CLI output.
type Task struct {
	ID           string       `json:"id"`
	Created      string       `json:"created"`
	Status       Status       `json:"status"`
	Body         string       `json:"body"`
	Priority     string       `json:"priority,omitzero"`
	Tags         []string     `json:"tags,omitempty"`
	CWD          string       `json:"cwd,omitzero"`
	Source       string       `json:"source,omitzero"`
	Agent        string       `json:"agent,omitzero"`
	GroupID      string       `json:"group_id,omitzero"`
	ResourceKey  string       `json:"resource_key,omitzero"`
	Started      string       `json:"started,omitzero"`
	LeaseExpires string       `json:"lease_expires,omitzero"`
	Finished     string       `json:"finished,omitzero"`
	Error        string       `json:"error,omitzero"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

// AddOptions carries metadata for a new task.
type AddOptions struct {
	Body        string
	Priority    string
	Tags        []string
	CWD         string
	Source      string
	Agent       string
	GroupID     string
	ResourceKey string
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
	}
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
	TaskID      string `json:"task_id"`
	DependsOnID string `json:"depends_on_id"`
	Created     string `json:"created"`
}

// ValidStatus reports whether s is a known status.
func ValidStatus(s Status) bool {
	_, ok := ParseStatus(string(s))
	return ok
}

// ParseStatus returns the canonical status for user or persisted input.
func ParseStatus(s string) (Status, bool) {
	switch Status(s) {
	case StatusPending, "pending":
		return StatusPending, true
	case StatusWorking, "working":
		return StatusWorking, true
	case StatusDone:
		return StatusDone, true
	case StatusFailed:
		return StatusFailed, true
	case StatusDeleted:
		return StatusDeleted, true
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
	case StatusPending, StatusWorking:
		return true
	default:
		return false
	}
}

// OrderedStatuses returns the canonical display order.
func OrderedStatuses() []Status {
	return []Status{StatusPending, StatusWorking, StatusDone, StatusFailed, StatusDeleted}
}

// MarkWorking claims the task for work and records the start timestamp.
func (t *Task) MarkWorking(now time.Time) {
	t.Status = StatusWorking
	t.Started = formatTime(now)
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
	return true
}

// MarkFailed marks the task failed. Already-failed tasks are left unchanged.
func (t *Task) MarkFailed(now time.Time, reason string) bool {
	if t.Status == StatusFailed {
		return false
	}
	t.Status = StatusFailed
	t.Finished = formatTime(now)
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
	t.Error = reason
	return true
}

// SetStatus applies a generic lifecycle transition.
func (t *Task) SetStatus(status Status, now time.Time, message string) bool {
	status = NormalizeStatus(status)
	switch status {
	case StatusPending:
		if t.Status == StatusPending {
			return false
		}
		t.Reset()
		return true
	case StatusWorking:
		if t.Status == StatusWorking {
			return false
		}
		t.MarkWorking(now)
		return true
	case StatusDone:
		return t.MarkDone(now)
	case StatusFailed:
		return t.MarkFailed(now, message)
	case StatusDeleted:
		return t.MarkDeleted(now, message)
	default:
		return false
	}
}

// Reset returns the task to todo and clears lifecycle/error fields.
func (t *Task) Reset() {
	t.Status = StatusPending
	t.Started = ""
	t.LeaseExpires = ""
	t.Finished = ""
	t.Error = ""
}

func formatTime(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}
