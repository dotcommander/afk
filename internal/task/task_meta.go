package task

import "time"

// statusMeta is the single source of truth mapping each canonical Status to
// its lifecycle Event and its transition function. Adding a new Status means
// adding one entry here — SetStatus and EventForStatus both read this table.
// Apply delegates to the Mark*/Reset methods so transition logic (including
// idempotency guards) lives in exactly one place; it returns whether the task
// changed. message is ignored by transitions that do not record a reason.
var statusMeta = map[Status]struct {
	Event EventType
	Apply func(t *Task, now time.Time, message string) bool
}{
	StatusTodo: {Event: EventRequeued, Apply: func(t *Task, _ time.Time, _ string) bool {
		if t.Status == StatusTodo {
			return false
		}
		t.Reset()
		return true
	}},
	StatusDoing: {Event: EventClaimed, Apply: func(t *Task, now time.Time, _ string) bool {
		if t.Status == StatusDoing {
			return false
		}
		t.MarkWorking(now)
		return true
	}},
	StatusDone: {Event: EventDone, Apply: func(t *Task, now time.Time, _ string) bool {
		return t.MarkDone(now)
	}},
	StatusFailed: {Event: EventFailed, Apply: func(t *Task, now time.Time, message string) bool {
		return t.MarkFailed(now, message)
	}},
	StatusDeleted: {Event: EventDeleted, Apply: func(t *Task, now time.Time, message string) bool {
		return t.MarkDeleted(now, message)
	}},
	StatusBudgetLimited: {Event: EventBudgetLimited, Apply: func(t *Task, now time.Time, message string) bool {
		return t.MarkBudgetLimited(now, message)
	}},
}

// init asserts at process start that every Status constant has a statusMeta
// entry. This converts the EventForStatus exhaustiveness check from a
// runtime-only panic (reached only when an unmapped status is exercised) into
// a startup-time failure: adding a Status constant without a statusMeta entry
// panics immediately on package load. EventForStatus retains its own panic as
// a last-resort fallback so the message still appears in stack traces.
func init() {
	for _, status := range AllStatuses() {
		if _, ok := statusMeta[status]; !ok {
			panic("task: status " + string(status) + " has no statusMeta entry — add it to statusMeta")
		}
	}
}

// EventForStatus returns the lifecycle Event for a validated Status. Callers
// MUST validate via ParseStatus first; an unknown status panics, signaling a
// Status constant added without a statusMeta entry rather than a runtime input
// bug.
func EventForStatus(status Status) EventType {
	meta, ok := statusMeta[status]
	if !ok {
		panic("task: EventForStatus called with unknown status " + string(status) + " — callers must validate via ParseStatus first")
	}
	return meta.Event
}
