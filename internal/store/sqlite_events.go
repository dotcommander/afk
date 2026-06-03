package store

import (
	"context"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// Events returns durable lifecycle events for a task.
func (s *SQLiteStore) Events(ctx context.Context, taskID string) ([]task.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, type, at, message
FROM task_events
WHERE task_id = ?
ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: events: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var events []task.Event
	for rows.Next() {
		var event task.Event
		var typ string
		if err := rows.Scan(&event.ID, &event.TaskID, &typ, &event.At, &event.Message); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		event.Type = task.EventType(typ)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: event rows: %w", err)
	}
	return events, nil
}

// Attempts returns execution attempts for a task.
func (s *SQLiteStore) Attempts(ctx context.Context, taskID string) ([]task.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, started, finished, status, error, worker_id, agent
FROM task_attempts
WHERE task_id = ?
ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: attempts: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var attempts []task.Attempt
	for rows.Next() {
		var attempt task.Attempt
		var status string
		if err := rows.Scan(&attempt.ID, &attempt.TaskID, &attempt.Started, &attempt.Finished, &status, &attempt.Error, &attempt.WorkerID, &attempt.Agent); err != nil {
			return nil, fmt.Errorf("store: scan attempt: %w", err)
		}
		attempt.Status = task.NormalizeStatus(task.Status(status))
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: attempt rows: %w", err)
	}
	return attempts, nil
}
