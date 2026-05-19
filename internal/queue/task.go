package queue

import "github.com/dotcommander/afk/internal/task"

// Status aliases are kept for queue package compatibility.
const (
	StatusPending = task.StatusPending
	StatusWorking = task.StatusWorking
	StatusDone    = task.StatusDone
	StatusFailed  = task.StatusFailed
)

// Task aliases the canonical task schema.
type Task = task.Task
