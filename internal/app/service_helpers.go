package app

import (
	"fmt"
	"os"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

func uniqueID(tasks []task.Task, base string) string {
	candidate := base
	for i := 1; ; i++ {
		if _, found := findTask(tasks, candidate); !found {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func findTask(tasks []task.Task, id string) (int, bool) {
	for i := range tasks {
		if tasks[i].ID == id {
			return i, true
		}
	}
	return -1, false
}

func filterByStatus(tasks []task.Task, status string) []task.Task {
	if status == "" {
		return tasks
	}
	out := tasks[:0:0]
	for _, t := range tasks {
		if string(t.Status) == status {
			out = append(out, t)
		}
	}
	return out
}

func formatTime(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}

func leaseExpires(now time.Time, lease time.Duration) time.Time {
	if lease <= 0 {
		return time.Time{}
	}
	return now.Add(lease)
}

func workerOrDefault(workerID string) string {
	if workerID != "" {
		return workerID
	}
	return defaultWorkerID()
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}
