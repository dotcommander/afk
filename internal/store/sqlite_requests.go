package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dotcommander/afk/internal/task"
)

const requestStateComplete = "complete"

type requestResult struct {
	ID   string    `json:"id"`
	Task task.Task `json:"task,omitzero"`
}

// UpdateRequested performs one task mutation and records its result in the
// same transaction as the event/attempt writes.
func (s *SQLiteStore) UpdateRequested(ctx context.Context, actor, requestID, operation, id string, event task.EventType, message string, mutate func(*task.Task) bool) (task.Task, bool, error) {
	actor, requestID, err := canonicalRequestKey(actor, requestID)
	if err != nil {
		return task.Task{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, false, fmt.Errorf("store: begin requested update: %w", err)
	}
	defer rollback(tx)
	resultJSON, replayed, err := s.beginRequest(ctx, tx, actor, requestID, operation)
	if err != nil {
		return task.Task{}, false, err
	}
	if replayed {
		var result requestResult
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
			return task.Task{}, false, fmt.Errorf("store: decode request result: %w", err)
		}
		return result.Task, true, nil
	}
	t, err := getTask(ctx, tx, id)
	if err != nil {
		return task.Task{}, false, err
	}
	if mutate(&t) {
		if err := s.applyRequestedUpdate(ctx, tx, t, event, message); err != nil {
			return task.Task{}, false, err
		}
	}
	encoded, err := json.Marshal(requestResult{ID: id, Task: t})
	if err != nil {
		return task.Task{}, false, fmt.Errorf("store: encode request result: %w", err)
	}
	if err := s.completeRequest(ctx, tx, actor, requestID, string(encoded)); err != nil {
		return task.Task{}, false, err
	}
	if err := commit(tx); err != nil {
		return task.Task{}, false, err
	}
	return t, false, nil
}

func (s *SQLiteStore) applyRequestedUpdate(ctx context.Context, tx *sql.Tx, t task.Task, event task.EventType, message string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET created=?, status=?, body=?, started=?, lease_expires=?, finished=?, error=?, priority=?, tags=?, cwd=?, source=?, agent=?, group_id=?, resource_key=?, stage=?, revision=revision+1 WHERE id=?`, t.Created, t.Status, t.Body, t.Started, t.LeaseExpires, t.Finished, t.Error, t.Priority, encodeTags(t.Tags), t.CWD, t.Source, t.Agent, t.GroupID, t.ResourceKey, t.Stage, t.ID); err != nil {
		return fmt.Errorf("store: requested update: %w", err)
	}
	at := s.eventTime(t)
	if err := s.insertEvent(ctx, tx, t.ID, event, at, message); err != nil {
		return err
	}
	if err := updateAttemptForEvent(ctx, tx, t, event, at, message); err != nil {
		return err
	}
	return refreshGoalStatus(ctx, tx, t.GroupID)
}

// AddRequested inserts a task and its optional dependency exactly once for an
// actor/request pair. The request row, task, event, dependency, and completion
// receipt commit together.
func (s *SQLiteStore) AddRequested(ctx context.Context, actor, requestID, operation string, t task.Task, dependsOnID string) (string, bool, error) {
	actor, requestID, err := canonicalRequestKey(actor, requestID)
	if err != nil {
		return "", false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("store: begin requested add: %w", err)
	}
	defer rollback(tx)

	resultJSON, replayed, err := s.beginRequest(ctx, tx, actor, requestID, operation)
	if err != nil {
		return "", false, err
	}
	if replayed {
		var result requestResult
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
			return "", false, fmt.Errorf("store: decode request result: %w", err)
		}
		return result.ID, true, nil
	}

	if err := s.insertTask(ctx, tx, t); err != nil {
		return "", false, err
	}
	if err := s.insertEvent(ctx, tx, t.ID, task.EventAdded, t.Created, ""); err != nil {
		return "", false, err
	}
	if dependsOnID != "" {
		if err := s.addDependencyInTx(ctx, tx, t.ID, dependsOnID); err != nil {
			return "", false, err
		}
	}
	encoded, err := json.Marshal(requestResult{ID: t.ID})
	if err != nil {
		return "", false, fmt.Errorf("store: encode request result: %w", err)
	}
	if err := s.completeRequest(ctx, tx, actor, requestID, string(encoded)); err != nil {
		return "", false, err
	}
	if err := commit(tx); err != nil {
		return "", false, err
	}
	return t.ID, false, nil
}

func (s *SQLiteStore) beginRequest(ctx context.Context, tx *sql.Tx, actor, requestID, operation string) (string, bool, error) {
	res, err := tx.ExecContext(ctx, `
INSERT INTO request_ledger (actor, request_id, operation, state, created_at)
VALUES (?, ?, ?, 'in_progress', ?) ON CONFLICT(actor, request_id) DO NOTHING`,
		actor, requestID, operation, s.nowString())
	if err != nil {
		return "", false, fmt.Errorf("store: begin request: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return "", false, fmt.Errorf("store: begin request rows: %w", err)
	}
	if inserted == 1 {
		return "", false, nil
	}
	var existingOperation, state, resultJSON string
	if err := tx.QueryRowContext(ctx, `
SELECT operation, state, result_json FROM request_ledger
WHERE actor = ? AND request_id = ?`, actor, requestID).Scan(&existingOperation, &state, &resultJSON); err != nil {
		return "", false, fmt.Errorf("store: load request: %w", err)
	}
	if existingOperation != operation {
		return "", false, fmt.Errorf("%w: actor=%q request_id=%q existing=%q requested=%q", ErrRequestCollision, actor, requestID, existingOperation, operation)
	}
	if state != requestStateComplete || resultJSON == "" {
		return "", false, fmt.Errorf("store: request %q for actor %q is incomplete", requestID, actor)
	}
	return resultJSON, true, nil
}

func canonicalRequestKey(actor, requestID string) (string, string, error) {
	actor = strings.TrimSpace(actor)
	requestID = strings.TrimSpace(requestID)
	if actor == "" || requestID == "" {
		return "", "", fmt.Errorf("store: actor and request id are required")
	}
	return actor, requestID, nil
}

func (s *SQLiteStore) completeRequest(ctx context.Context, tx *sql.Tx, actor, requestID, resultJSON string) error {
	res, err := tx.ExecContext(ctx, `
UPDATE request_ledger
SET state = ?, result_json = ?, completed_at = ?
WHERE actor = ? AND request_id = ? AND state = 'in_progress'`,
		requestStateComplete, resultJSON, s.nowString(), actor, requestID)
	if err != nil {
		return fmt.Errorf("store: complete request: %w", err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: complete request rows: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("store: request completion lost for actor=%q request_id=%q", actor, requestID)
	}
	return nil
}

func (s *SQLiteStore) addDependencyInTx(ctx context.Context, tx *sql.Tx, taskID, dependsOnID string) error {
	relType, err := validateRelation(taskID, dependsOnID, task.RelationBlocks)
	if err != nil {
		return err
	}
	if _, err := getTask(ctx, tx, dependsOnID); err != nil {
		return err
	}
	cycle, err := dependencyPathExists(ctx, tx, dependsOnID, taskID)
	if err != nil {
		return err
	}
	if cycle {
		return ErrDependencyCycle
	}
	edge := relationEdge{taskID: taskID, relatedID: dependsOnID, relType: relType, created: s.nowString()}
	if err := s.insertRelation(ctx, tx, edge); err != nil {
		return err
	}
	return s.recordRelationEvent(ctx, tx, edge, "")
}
