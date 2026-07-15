// Package store defines persistence operations for tasks.
package store

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when a task id is absent.
var ErrNotFound = errors.New("task not found")

// ErrInvalidDependency is returned when a dependency edge is malformed.
var ErrInvalidDependency = errors.New("invalid dependency")

// ErrDependencyCycle is returned when a dependency edge would create a cycle.
var ErrDependencyCycle = errors.New("dependency cycle")

// ErrDependencyNotFound is returned when a dependency edge is absent.
var ErrDependencyNotFound = errors.New("dependency not found")

// ErrInvalidState is returned when an operation does not apply to the current task state.
var ErrInvalidState = errors.New("invalid state")

// ErrDuplicateTask is returned when a task id already exists.
var ErrDuplicateTask = errors.New("duplicate task")

// ErrWorkerMismatch is returned when worker-owned state is modified by another worker.
var ErrWorkerMismatch = errors.New("worker mismatch")

// ErrRequestCollision reports reuse of one actor/request id for a different operation.
var ErrRequestCollision = errors.New("request id collision")

// ConflictError reports a failed optimistic write against a stale task revision.
type ConflictError struct {
	TaskID   string
	Expected int64
	Current  int64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("task %s revision conflict: expected %d, current %d", e.TaskID, e.Expected, e.Current)
}
