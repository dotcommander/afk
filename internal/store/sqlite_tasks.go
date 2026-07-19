package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// List returns all tasks in insertion order.
func (s *SQLiteStore) List(ctx context.Context) ([]task.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, created, status, body, started, lease_expires, finished, error,
	priority, tags, cwd, source, agent, group_id, resource_key, stage, available_at
FROM tasks
ORDER BY ordinal, rowid`)
	if err != nil {
		return nil, fmt.Errorf("store: list: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var tasks []task.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list rows: %w", err)
	}
	return tasks, nil
}

// Get returns a single task by id, using the primary-key index.
func (s *SQLiteStore) Get(ctx context.Context, id string) (task.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, fmt.Errorf("store: begin get: %w", err)
	}
	defer rollback(tx)
	t, err := getTask(ctx, tx, id)
	if err != nil {
		return task.Task{}, err
	}
	if err := commit(tx); err != nil {
		return task.Task{}, err
	}
	return t, nil
}

// Add appends t to the queue.
func (s *SQLiteStore) Add(ctx context.Context, t task.Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin add: %w", err)
	}
	defer rollback(tx)
	if err := s.insertTask(ctx, tx, t); err != nil {
		return err
	}
	if err := s.insertEvent(ctx, tx, t.ID, task.EventAdded, t.Created, ""); err != nil {
		return err
	}
	return commit(tx)
}

// AddWithDependency inserts t and its blocking dependency in one transaction.
func (s *SQLiteStore) AddWithDependency(ctx context.Context, t task.Task, dependsOnID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin add with dependency: %w", err)
	}
	defer rollback(tx)
	if err := s.insertTask(ctx, tx, t); err != nil {
		return err
	}
	if err := s.insertEvent(ctx, tx, t.ID, task.EventAdded, t.Created, ""); err != nil {
		return err
	}
	if err := s.addDependencyInTx(ctx, tx, t.ID, dependsOnID); err != nil {
		return err
	}
	return commit(tx)
}

func (s *SQLiteStore) insertTask(ctx context.Context, tx *sql.Tx, t task.Task) error {
	ordinal, err := nextOrdinal(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO tasks (
	id, created, status, body, started, finished, error, ordinal,
	priority, tags, cwd, source, agent, group_id, resource_key, available_at, stage
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Created, string(t.Status), t.Body, t.Started, t.Finished, t.Error, ordinal,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.AvailableAt, t.Stage); err != nil {
		if isDuplicateTaskID(err) {
			return fmt.Errorf("store: add task %s: %w", t.ID, ErrDuplicateTask)
		}
		return fmt.Errorf("store: add task %s: %w", t.ID, err)
	}
	return nil
}

// Update mutates one task. If fn returns false, no write occurs.
func (s *SQLiteStore) Update(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error {
	_, err := s.updateImpl(ctx, taskUpdate{id: id, event: event, message: message, mutate: fn})
	return err
}

// UpdateGuarded rejects an unfenced terminal mutation while a named worker
// owns the active attempt. It is the default path for operator-facing set
// commands; Update remains available for internal administrative operations.
func (s *SQLiteStore) UpdateGuarded(ctx context.Context, id string, event task.EventType, message string, fn func(*task.Task) bool) error {
	_, err := s.updateImpl(ctx, taskUpdate{id: id, event: event, message: message, mutate: fn, guardOwned: true})
	return err
}

type taskUpdate struct {
	id           string
	expectWorker string
	event        task.EventType
	message      string
	mutate       func(*task.Task) bool
	guardOwned   bool
}

func (s *SQLiteStore) updateImpl(ctx context.Context, update taskUpdate) (task.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, fmt.Errorf("store: begin update: %w", err)
	}
	defer rollback(tx)
	t, err := getTask(ctx, tx, update.id)
	if err != nil {
		return task.Task{}, err
	}
	if err := s.checkUpdateFence(ctx, tx, t, update.expectWorker, update.event, update.guardOwned); err != nil {
		return task.Task{}, err
	}
	if !update.mutate(&t) {
		return t, nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE tasks
SET created = ?, status = ?, body = ?, started = ?, lease_expires = ?, finished = ?, error = ?,
	priority = ?, tags = ?, cwd = ?, source = ?, agent = ?, group_id = ?, resource_key = ?, stage = ?, available_at = ?, revision = revision + 1
WHERE id = ?`,
		t.Created, string(t.Status), t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error,
		t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage, t.AvailableAt, t.ID); err != nil {
		return task.Task{}, fmt.Errorf("store: update task %s: %w", update.id, err)
	}
	at := s.eventTime(t)
	if err := s.insertEvent(ctx, tx, update.id, update.event, at, update.message); err != nil {
		return task.Task{}, err
	}
	if err := updateAttemptForEvent(ctx, tx, t, update.event, at, update.message); err != nil {
		return task.Task{}, err
	}
	if err := refreshGoalStatus(ctx, tx, t.GroupID); err != nil {
		return task.Task{}, err
	}
	if err := commit(tx); err != nil {
		return task.Task{}, err
	}
	return t, nil
}

// checkUpdateFence rejects terminal writes to an owned active attempt unless
// the caller supplies that attempt's worker id. The check runs in the same
// transaction as the mutation so a stale worker cannot race a re-claim.
func (s *SQLiteStore) checkUpdateFence(ctx context.Context, tx *sql.Tx, t task.Task, expectWorker string, event task.EventType, guardOwned bool) error {
	if expectWorker != "" {
		return s.checkExplicitWorkerFence(ctx, tx, t, expectWorker)
	}
	if !guardOwned || t.Status != task.StatusDoing || !terminalEvent(event) {
		return nil
	}
	owner, err := activeAttemptOwner(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if owner != "" {
		return ErrWorkerMismatch
	}
	return nil
}

func (s *SQLiteStore) checkExplicitWorkerFence(ctx context.Context, tx *sql.Tx, t task.Task, expectWorker string) error {
	if t.Status != task.StatusDoing {
		return ErrWorkerMismatch
	}
	if t.LeaseExpires != "" {
		deadline, err := time.Parse(time.RFC3339, t.LeaseExpires)
		if err != nil || !deadline.After(s.now().UTC()) {
			return ErrWorkerMismatch
		}
	}
	owner, err := activeAttemptOwner(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if owner != expectWorker {
		return ErrWorkerMismatch
	}
	return nil
}

func activeAttemptOwner(ctx context.Context, tx *sql.Tx, taskID string) (string, error) {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT worker_id FROM task_attempts WHERE task_id=? AND finished='' ORDER BY id DESC LIMIT 1`, taskID).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: fence owner lookup %s: %w", taskID, err)
	}
	return owner, nil
}

func terminalEvent(event task.EventType) bool {
	return event == task.EventDone || event == task.EventFailed
}

// UpdateFenced mutates a task only when expectWorker owns its active attempt.
func (s *SQLiteStore) UpdateFenced(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) error {
	_, err := s.UpdateFencedTask(ctx, id, expectWorker, event, message, fn)
	return err
}

// UpdateFencedTask mutates a worker-owned task and returns the committed snapshot.
func (s *SQLiteStore) UpdateFencedTask(ctx context.Context, id string, expectWorker string, event task.EventType, message string, fn func(*task.Task) bool) (task.Task, error) {
	return s.updateImpl(ctx, taskUpdate{id: id, expectWorker: expectWorker, event: event, message: message, mutate: fn})
}

// Delete removes the task with id.
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin delete: %w", err)
	}
	defer rollback(tx)
	t, err := getTask(ctx, tx, id)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete task %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	if err := s.insertEvent(ctx, tx, id, task.EventRemoved, "", ""); err != nil {
		return err
	}
	if t.GroupID != "" {
		if err := refreshGoalStatusWithFallback(ctx, tx, t.GroupID, "failed"); err != nil {
			return err
		}
	}
	return commit(tx)
}
