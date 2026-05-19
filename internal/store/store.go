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

// Store persists tasks and owns atomic queue operations.
type Store interface {
	List(ctx context.Context) ([]task.Task, error)
	Add(ctx context.Context, t task.Task) error
	Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error
	Delete(ctx context.Context, id string) error
	Prune(ctx context.Context, statuses []task.Status) error
	ClaimNext(ctx context.Context, now time.Time, leaseExpires time.Time) (*task.Task, error)
	Events(ctx context.Context, taskID string) ([]task.Event, error)
	Attempts(ctx context.Context, taskID string) ([]task.Attempt, error)
	RequeueStale(ctx context.Context, olderThan time.Duration, now time.Time) ([]task.Task, error)
}
