package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// ClaimNext atomically marks the first ready todo task doing and returns it.
func (s *SQLiteStore) ClaimNext(ctx context.Context, now time.Time, leaseExpires time.Time) (*task.Task, error) {
	return s.ClaimNextForWorker(ctx, now, leaseExpires, "", "")
}

// ClaimNextForWorker atomically marks the first ready task doing and records worker metadata.
func (s *SQLiteStore) ClaimNextForWorker(ctx context.Context, now time.Time, leaseExpires time.Time, workerID, agent string) (*task.Task, error) {
	started := now.UTC().Format(time.RFC3339)
	lease := ""
	if !leaseExpires.IsZero() {
		lease = leaseExpires.UTC().Format(time.RFC3339)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin claim: %w", err)
	}
	defer rollback(tx)

	row := tx.QueryRowContext(ctx, `
UPDATE tasks
SET status = ?, started = ?, lease_expires = ?
WHERE id = (
	SELECT id
	FROM tasks
	WHERE status = ?`+readyWhereSQL+`
	ORDER BY `+schedulerOrderSQL+`
	LIMIT 1
)
RETURNING id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage`,
		string(task.StatusDoing), started, lease, string(task.StatusTodo), string(task.StatusDone), string(task.StatusDoing))
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := s.insertEvent(ctx, tx, t.ID, task.EventClaimed, started, ""); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_attempts (task_id, started, status, error, worker_id, agent)
VALUES (?, ?, ?, ?, ?, ?)`, t.ID, started, string(task.StatusDoing), "", workerID, agent); err != nil {
		return nil, fmt.Errorf("store: insert attempt: %w", err)
	}
	if err := commit(tx); err != nil {
		return nil, err
	}
	return &t, nil
}

// Heartbeat extends the lease for a worker-owned active attempt.
func (s *SQLiteStore) Heartbeat(ctx context.Context, taskID, workerID string, now time.Time, leaseExpires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin heartbeat: %w", err)
	}
	defer rollback(tx)

	t, err := getTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if t.Status != task.StatusDoing {
		return ErrInvalidState
	}
	var owner string
	err = tx.QueryRowContext(ctx, `
SELECT worker_id
FROM task_attempts
WHERE task_id = ? AND finished = ''
ORDER BY id DESC
LIMIT 1`, taskID).Scan(&owner)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidState
		}
		return fmt.Errorf("store: heartbeat owner %s: %w", taskID, err)
	}
	if owner != workerID {
		return ErrWorkerMismatch
	}
	lease := ""
	if !leaseExpires.IsZero() {
		lease = leaseExpires.UTC().Format(time.RFC3339)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE tasks
SET lease_expires = ?
WHERE id = ?`, lease, taskID); err != nil {
		return fmt.Errorf("store: heartbeat update %s: %w", taskID, err)
	}
	if err := s.insertEvent(ctx, tx, taskID, task.EventHeartbeat, now.UTC().Format(time.RFC3339), workerID); err != nil {
		return err
	}
	return commit(tx)
}

// RequeueStale resets doing tasks whose lease expired or whose start time is older than olderThan.
func (s *SQLiteStore) RequeueStale(ctx context.Context, olderThan time.Duration, now time.Time) ([]task.Task, error) {
	cutoff := now.Add(-olderThan).UTC().Format(time.RFC3339)
	nowText := now.UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage
FROM tasks
WHERE status = ?
AND (
	(lease_expires != '' AND lease_expires <= ?)
	OR (lease_expires = '' AND started != '' AND started <= ?)
)
ORDER BY ordinal, rowid`, string(task.StatusDoing), nowText, cutoff)
	if err != nil {
		return nil, fmt.Errorf("store: list stale: %w", err)
	}
	var stale []task.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		stale = append(stale, t)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close stale rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: stale rows: %w", err)
	}
	for _, t := range stale {
		id := t.ID
		if err := s.Update(ctx, id, task.EventRequeued, "stale", func(tk *task.Task) bool {
			tk.Reset()
			return true
		}); err != nil {
			return nil, err
		}
	}
	return stale, nil
}
