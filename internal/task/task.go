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
	StatusPending Status = "pending"
	StatusWorking Status = "working"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// ErrInvalidStatus reports an unknown task status.
var ErrInvalidStatus = errors.New("invalid task status")

// Task is the JSONL schema. Field order matches the existing /qadd producer.
type Task struct {
	ID           string   `json:"id"`
	Created      string   `json:"created"`
	Status       Status   `json:"status"`
	Body         string   `json:"body"`
	Priority     string   `json:"priority,omitzero"`
	Tags         []string `json:"tags,omitempty"`
	CWD          string   `json:"cwd,omitzero"`
	Source       string   `json:"source,omitzero"`
	Agent        string   `json:"agent,omitzero"`
	GroupID      string   `json:"group_id,omitzero"`
	ResourceKey  string   `json:"resource_key,omitzero"`
	Started      string   `json:"started,omitzero"`
	LeaseExpires string   `json:"lease_expires,omitzero"`
	Finished     string   `json:"finished,omitzero"`
	Error        string   `json:"error,omitzero"`
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

// EventType is a durable lifecycle event name.
type EventType string

const (
	EventAdded             EventType = "added"
	EventClaimed           EventType = "claimed"
	EventDone              EventType = "done"
	EventFailed            EventType = "failed"
	EventReset             EventType = "reset"
	EventEdited            EventType = "edited"
	EventRemoved           EventType = "removed"
	EventPruned            EventType = "pruned"
	EventRetried           EventType = "retried"
	EventRequeued          EventType = "requeued"
	EventHeartbeat         EventType = "heartbeat"
	EventBlocked           EventType = "blocked"
	EventUnblocked         EventType = "unblocked"
	EventDependencyAdded   EventType = "dependency_added"
	EventDependencyRemoved EventType = "dependency_removed"
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

// Block records a manual scheduling block for a task.
type Block struct {
	TaskID    string `json:"task_id"`
	Reason    string `json:"reason"`
	Created   string `json:"created"`
	CreatedBy string `json:"created_by,omitzero"`
}

// ValidStatus reports whether s is a known status.
func ValidStatus(s Status) bool {
	switch s {
	case StatusPending, StatusWorking, StatusDone, StatusFailed:
		return true
	default:
		return false
	}
}

// OrderedStatuses returns the canonical display order.
func OrderedStatuses() []Status {
	return []Status{StatusPending, StatusWorking, StatusDone, StatusFailed}
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

// Reset returns the task to pending and clears lifecycle/error fields.
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
