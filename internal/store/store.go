// Package store defines persistence operations for tasks.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// ErrNotFound is returned when a task id is absent.
var ErrNotFound = errors.New("task not found")

// ErrInvalidDependency is returned when a dependency edge is malformed.
var ErrInvalidDependency = errors.New("invalid dependency")

// ErrDependencyCycle is returned when a dependency edge would create a cycle.
var ErrDependencyCycle = errors.New("dependency cycle")

// ErrDependencyNotFound is returned when a dependency edge is absent.
var ErrDependencyNotFound = errors.New("dependency not found")

// ErrBlockNotFound is returned when a manual block is absent.
var ErrBlockNotFound = errors.New("block not found")

// ErrInvalidState is returned when an operation does not apply to the current task state.
var ErrInvalidState = errors.New("invalid state")

// ErrWorkerMismatch is returned when worker-owned state is modified by another worker.
var ErrWorkerMismatch = errors.New("worker mismatch")

// ReadyOptions controls ready-task selection.
type ReadyOptions struct {
	Now time.Time
}

// Store persists tasks and owns atomic queue operations.
type Store interface {
	List(ctx context.Context) ([]task.Task, error)
	Ready(ctx context.Context, opts ReadyOptions) ([]task.Task, error)
	Add(ctx context.Context, t task.Task) error
	Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error
	Delete(ctx context.Context, id string) error
	Prune(ctx context.Context, statuses []task.Status) error
	// PruneByTag deletes all tasks whose tags slice contains the given tag.
	// Returns the number of deleted tasks. Returns an error if tag is empty.
	PruneByTag(ctx context.Context, tag string) (int, error)
	ClaimNext(ctx context.Context, now time.Time, leaseExpires time.Time) (*task.Task, error)
	ClaimNextForWorker(ctx context.Context, now time.Time, leaseExpires time.Time, workerID, agent string) (*task.Task, error)
	Heartbeat(ctx context.Context, taskID, workerID string, now time.Time, leaseExpires time.Time) error
	AddDependency(ctx context.Context, taskID, dependsOnID string) error
	RemoveDependency(ctx context.Context, taskID, dependsOnID string) error
	Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error)
	BulkAdd(ctx context.Context, tasks []task.Task, deps []task.Dependency) error
	Block(ctx context.Context, taskID, reason string) error
	Unblock(ctx context.Context, taskID string) error
	BlockForTask(ctx context.Context, taskID string) (*task.Block, error)
	Events(ctx context.Context, taskID string) ([]task.Event, error)
	Attempts(ctx context.Context, taskID string) ([]task.Attempt, error)
	RequeueStale(ctx context.Context, olderThan time.Duration, now time.Time) ([]task.Task, error)
}
