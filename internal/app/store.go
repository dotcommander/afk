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
	Get(ctx context.Context, id string) (task.Task, error)
	Counts(ctx context.Context) (map[task.Status]int, error)
	ActiveLists(ctx context.Context) (todo, doing []task.Task, err error)
	Ready(ctx context.Context) ([]task.Task, error)
	Add(ctx context.Context, t task.Task) error
	Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error
	Delete(ctx context.Context, id string) error
	// Prune physically removes matching rows. Public callers should prefer
	// status=deleted so task history remains inspectable.
	Prune(ctx context.Context, statuses []task.Status) (int, error)
	ClaimNextForWorker(ctx context.Context, now time.Time, leaseExpires time.Time, workerID, agent string) (*task.Task, error)
	Heartbeat(ctx context.Context, taskID, workerID string, now time.Time, leaseExpires time.Time) error
	AddDependency(ctx context.Context, taskID, dependsOnID string) error
	Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error)
	AddGate(ctx context.Context, taskID, name string) error
	SatisfyGate(ctx context.Context, taskID, name string) error
	Gates(ctx context.Context, taskID string) ([]task.Gate, error)
	Events(ctx context.Context, taskID string) ([]task.Event, error)
	Attempts(ctx context.Context, taskID string) ([]task.Attempt, error)
	RequeueStale(ctx context.Context, olderThan time.Duration, now time.Time) ([]task.Task, error)
}

var _ Store = (*store.SQLiteStore)(nil)
