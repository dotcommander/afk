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
SET status = ?, started = ?, lease_expires = ?, revision = revision + 1
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
	if err := refreshGoalStatus(ctx, tx, t.GroupID); err != nil {
		return nil, err
	}
	if err := commit(tx); err != nil {
		return nil, err
	}
	return &t, nil
}

// ClaimTaskForWorker atomically satisfies the named gates and claims one
// explicit task only when the resulting task is ready. A retry by the same
// worker returns its existing active claim without adding another attempt.
func (s *SQLiteStore) ClaimTaskForWorker(ctx context.Context, id string, now, leaseExpires time.Time, workerID, agent string, satisfyGates []string) (*task.Task, error) {
	started := now.UTC().Format(time.RFC3339)
	lease := ""
	if !leaseExpires.IsZero() {
		lease = leaseExpires.UTC().Format(time.RFC3339)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin explicit claim: %w", err)
	}
	defer rollback(tx)
	for _, gate := range satisfyGates {
		if _, err := tx.ExecContext(ctx, `UPDATE task_gates SET satisfied=1, satisfied_at=? WHERE task_id=? AND name=? AND satisfied=0`, started, id, gate); err != nil {
			return nil, fmt.Errorf("store: satisfy claim gate %q: %w", gate, err)
		}
	}
	row := tx.QueryRowContext(ctx, `
UPDATE tasks
SET status=?, started=?, lease_expires=?, revision=revision+1
WHERE id=? AND status=?`+readyWhereSQL+`
RETURNING id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage`,
		string(task.StatusDoing), started, lease, id, string(task.StatusTodo), string(task.StatusDone), string(task.StatusDoing))
	claimed, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		existing, replayErr := explicitClaimReplay(ctx, tx, id, workerID)
		if replayErr != nil {
			return nil, replayErr
		}
		if err := commit(tx); err != nil {
			return nil, err
		}
		return &existing, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.insertEvent(ctx, tx, claimed.ID, task.EventClaimed, started, ""); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_attempts (task_id,started,status,error,worker_id,agent) VALUES (?,?,?,?,?,?)`, claimed.ID, started, string(task.StatusDoing), "", workerID, agent); err != nil {
		return nil, fmt.Errorf("store: insert explicit claim attempt: %w", err)
	}
	if err := refreshGoalStatus(ctx, tx, claimed.GroupID); err != nil {
		return nil, err
	}
	if err := commit(tx); err != nil {
		return nil, err
	}
	return &claimed, nil
}

func explicitClaimReplay(ctx context.Context, tx *sql.Tx, id, workerID string) (task.Task, error) {
	existing, err := getTask(ctx, tx, id)
	if err != nil {
		return task.Task{}, err
	}
	if existing.Status != task.StatusDoing {
		return task.Task{}, ErrInvalidState
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT worker_id FROM task_attempts WHERE task_id=? AND finished='' ORDER BY id DESC LIMIT 1`, id).Scan(&owner); err != nil {
		return task.Task{}, fmt.Errorf("store: explicit claim owner: %w", err)
	}
	if owner != workerID {
		return task.Task{}, ErrWorkerMismatch
	}
	return existing, nil
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
SET lease_expires = ?, revision = revision + 1
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
	requeued := make([]task.Task, 0, len(stale))
	for _, t := range stale {
		result, err := s.requeueIfStillStale(ctx, t.ID, cutoff, nowText)
		if err != nil {
			return nil, err
		}
		if result.requeued {
			requeued = append(requeued, result.prior)
		}
	}
	return requeued, nil
}

type staleRequeueResult struct {
	prior    task.Task
	requeued bool
}

func (s *SQLiteStore) requeueIfStillStale(ctx context.Context, id, cutoff, nowText string) (staleRequeueResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return staleRequeueResult{}, fmt.Errorf("store: begin requeue stale: %w", err)
	}
	defer rollback(tx)

	t, err := getTask(ctx, tx, id)
	if err != nil {
		return staleRequeueResult{}, err
	}
	if !taskStillStale(t, cutoff, nowText) {
		return staleRequeueResult{}, nil
	}
	prior := t
	t.Reset()
	if _, err := tx.ExecContext(ctx, `
UPDATE tasks
SET created = ?, status = ?, body = ?, started = ?, lease_expires = ?, finished = ?, error = ?,
	priority = ?, tags = ?, cwd = ?, source = ?, agent = ?, group_id = ?, resource_key = ?, stage = ?, revision = revision + 1
WHERE id = ?`,
		t.Created, string(t.Status), t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage, t.ID); err != nil {
		return staleRequeueResult{}, fmt.Errorf("store: requeue stale task %s: %w", id, err)
	}
	at := s.eventTime(t)
	if err := s.insertEvent(ctx, tx, id, task.EventRequeued, at, "stale"); err != nil {
		return staleRequeueResult{}, err
	}
	if err := updateAttemptForEvent(ctx, tx, t, task.EventRequeued, at, "stale"); err != nil {
		return staleRequeueResult{}, err
	}
	if err := refreshGoalStatus(ctx, tx, t.GroupID); err != nil {
		return staleRequeueResult{}, err
	}
	if err := commit(tx); err != nil {
		return staleRequeueResult{}, err
	}
	return staleRequeueResult{prior: prior, requeued: true}, nil
}

func taskStillStale(t task.Task, cutoff, nowText string) bool {
	if t.Status != task.StatusDoing {
		return false
	}
	if t.LeaseExpires != "" {
		return t.LeaseExpires <= nowText
	}
	return t.Started != "" && t.Started <= cutoff
}
