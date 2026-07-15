package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// AddCheckpoint appends a task checkpoint. Provenance is immutable on replay.
func (s *SQLiteStore) AddCheckpoint(ctx context.Context, c task.Checkpoint) (task.Checkpoint, error) {
	err := s.withTaskRecordTx(ctx, c.TaskID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO task_checkpoints
(task_id, kind, checkpoint_key, value_json, source_system, source_id, source_event_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, c.TaskID, c.Kind, c.Key, c.ValueJSON, c.Provenance.System, c.Provenance.ID, c.Provenance.EventID, c.CreatedAt)
		if err != nil {
			return fmt.Errorf("store: add checkpoint: %w", err)
		}
		c.ID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("store: checkpoint id: %w", err)
		}
		return nil
	})
	return c, err
}

// AddArtifact appends a task artifact. Provenance is immutable on replay.
func (s *SQLiteStore) AddArtifact(ctx context.Context, a task.Artifact) error {
	return s.withTaskRecordTx(ctx, a.TaskID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO task_artifacts
(id, task_id, path, content_type, metadata_json, source_system, source_id, source_event_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.ID, a.TaskID, a.Path, a.ContentType, a.MetadataJSON, a.Provenance.System, a.Provenance.ID, a.Provenance.EventID, a.CreatedAt)
		if err != nil {
			return fmt.Errorf("store: add artifact: %w", err)
		}
		return nil
	})
}

func (s *SQLiteStore) withTaskRecordTx(ctx context.Context, taskID string, write func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin task record: %w", err)
	}
	defer rollback(tx)
	if _, err := getTask(ctx, tx, taskID); err != nil {
		return err
	}
	if err := write(tx); err != nil {
		return err
	}
	return commit(tx)
}

// Checkpoints returns task progress records in insertion order.
func (s *SQLiteStore) Checkpoints(ctx context.Context, taskID string) ([]task.Checkpoint, error) { //nolint:dupl // record-specific scanner mirrors Artifact intentionally
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin checkpoint list: %w", err)
	}
	defer rollback(tx)
	if _, err := getTask(ctx, tx, taskID); err != nil {
		return nil, err
	}
	rows, err := queryTaskRecords(ctx, tx, `SELECT id, task_id, kind, checkpoint_key, value_json, source_system, source_id, source_event_id, created_at FROM task_checkpoints WHERE task_id = ? ORDER BY id`, taskID, "checkpoint", func(rows *sql.Rows) (task.Checkpoint, error) {
		var c task.Checkpoint
		var eventID sql.NullInt64
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Kind, &c.Key, &c.ValueJSON, &c.Provenance.System, &c.Provenance.ID, &eventID, &c.CreatedAt); err != nil {
			return c, err
		}
		if eventID.Valid {
			c.Provenance.EventID = &eventID.Int64
		}
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	if err := commit(tx); err != nil {
		return nil, err
	}
	return rows, nil
}

// Artifacts returns task artifacts in source order.
func (s *SQLiteStore) Artifacts(ctx context.Context, taskID string) ([]task.Artifact, error) { //nolint:dupl // record-specific scanner mirrors Checkpoint intentionally
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin artifact list: %w", err)
	}
	defer rollback(tx)
	if _, err := getTask(ctx, tx, taskID); err != nil {
		return nil, err
	}
	rows, err := queryTaskRecords(ctx, tx, `SELECT id, task_id, path, content_type, metadata_json, source_system, source_id, source_event_id, created_at FROM task_artifacts WHERE task_id = ? ORDER BY created_at, id`, taskID, "artifact", func(rows *sql.Rows) (task.Artifact, error) {
		var a task.Artifact
		var eventID sql.NullInt64
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Path, &a.ContentType, &a.MetadataJSON, &a.Provenance.System, &a.Provenance.ID, &eventID, &a.CreatedAt); err != nil {
			return a, err
		}
		if eventID.Valid {
			a.Provenance.EventID = &eventID.Int64
		}
		return a, nil
	})
	if err != nil {
		return nil, err
	}
	if err := commit(tx); err != nil {
		return nil, err
	}
	return rows, nil
}

type rowQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryTaskRecords[T any](ctx context.Context, db rowQueryer, query, taskID, kind string, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: list %ss: %w", kind, err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err below is authoritative
	var out []T
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan %s: %w", kind, err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list %ss: %w", kind, err)
	}
	if out == nil {
		out = make([]T, 0)
	}
	return out, nil
}

// UpdateCAS applies a stale-read-sensitive mutation only at expectedRevision.
func (s *SQLiteStore) UpdateCAS(ctx context.Context, id string, expectedRevision int64, event task.EventType, message string, mutate func(*task.Task) bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin CAS update: %w", err)
	}
	defer rollback(tx)
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM tasks WHERE id = ?`, id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("task %s: %w", id, ErrNotFound)
		}
		return 0, fmt.Errorf("store: read revision: %w", err)
	}
	if current != expectedRevision {
		return current, &ConflictError{TaskID: id, Expected: expectedRevision, Current: current}
	}
	t, err := getTask(ctx, tx, id)
	if err != nil {
		return current, err
	}
	if !mutate(&t) {
		return current, nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE tasks SET created=?, status=?, body=?, started=?, lease_expires=?, finished=?, error=?, priority=?, tags=?, cwd=?, source=?, agent=?, group_id=?, resource_key=?, stage=?, revision=revision+1 WHERE id=? AND revision=?`, t.Created, t.Status, t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error, t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage, id, expectedRevision)
	if err != nil {
		return current, fmt.Errorf("store: CAS update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return current, &ConflictError{TaskID: id, Expected: expectedRevision, Current: current}
	}
	at := s.eventTime(t)
	if err := s.insertEvent(ctx, tx, id, event, at, message); err != nil {
		return current, err
	}
	if err := updateAttemptForEvent(ctx, tx, t, event, at, message); err != nil {
		return current, err
	}
	if err := refreshGoalStatus(ctx, tx, t.GroupID); err != nil {
		return current, err
	}
	if err := commit(tx); err != nil {
		return current, err
	}
	return expectedRevision + 1, nil
}
