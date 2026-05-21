package app

import (
	"context"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
)

// Store persists tasks and owns atomic queue operations.
// Defined here (where consumed) per Go interface idiom.
type Store interface {
	List(ctx context.Context) ([]task.Task, error)
	Ready(ctx context.Context) ([]task.Task, error)
	Add(ctx context.Context, t task.Task) error
	Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error
	Delete(ctx context.Context, id string) error
	Prune(ctx context.Context, statuses []task.Status) error
	Promote(ctx context.Context, id string) error
	// PruneByTag deletes all tasks whose tags slice contains the given tag.
	// Returns the number of deleted tasks. Returns an error if tag is empty.
	PruneByTag(ctx context.Context, tag string) (int, error)
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

var _ Store = (*store.SQLiteStore)(nil)
