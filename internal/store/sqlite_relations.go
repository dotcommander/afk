package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// AddDependency records that taskID is blocked by dependsOnID. It is a thin
// wrapper over AddRelation with the blocks relation, keeping a single
// insert/cycle/event code path.
func (s *SQLiteStore) AddDependency(ctx context.Context, taskID, dependsOnID string) error {
	return s.AddRelation(ctx, taskID, dependsOnID, task.RelationBlocks)
}

// AddRelation records a typed edge from taskID to relatedID. Only blocks edges
// gate readiness (see readyWhereSQL) and are subject to cycle detection;
// relates/duplicates/parent are informational. Re-adding an existing edge
// updates its relation type. An empty relType defaults to RelationBlocks.
func (s *SQLiteStore) AddRelation(ctx context.Context, taskID, relatedID string, relType task.RelationType) error {
	if taskID == "" || relatedID == "" {
		return ErrInvalidDependency
	}
	if taskID == relatedID {
		return fmt.Errorf("task %s: cannot relate to itself: %w", taskID, task.ErrInvalidRelation)
	}
	relType, err := task.ParseRelationType(string(relType))
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin add relation: %w", err)
	}
	defer rollback(tx)

	if _, err := getTask(ctx, tx, taskID); err != nil {
		return err
	}
	if _, err := getTask(ctx, tx, relatedID); err != nil {
		return err
	}

	// Only blocks edges participate in scheduling; non-blocking edges cannot
	// create a readiness cycle, so skip cycle detection for them.
	if relType == task.RelationBlocks {
		cycle, err := dependencyPathExists(ctx, tx, relatedID, taskID)
		if err != nil {
			return err
		}
		if cycle {
			return ErrDependencyCycle
		}
	}

	// Read the prior type (if any) so a no-op re-add emits no event, matching
	// the historical idempotent AddDependency contract.
	var prior string
	priorErr := tx.QueryRowContext(ctx,
		`SELECT relation_type FROM task_dependencies WHERE task_id = ? AND depends_on_id = ?`,
		taskID, relatedID).Scan(&prior)
	switch {
	case errors.Is(priorErr, sql.ErrNoRows):
		prior = ""
	case priorErr != nil:
		return fmt.Errorf("store: read relation %s -> %s: %w", taskID, relatedID, priorErr)
	}

	created := s.nowString()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_dependencies (task_id, depends_on_id, created, relation_type)
VALUES (?, ?, ?, ?)
ON CONFLICT(task_id, depends_on_id) DO UPDATE SET relation_type = excluded.relation_type`,
		taskID, relatedID, created, string(relType)); err != nil {
		return fmt.Errorf("store: add relation %s -> %s: %w", taskID, relatedID, err)
	}

	// A re-add that does not change the relation type is a no-op: emit no event.
	if prior == string(relType) {
		return commit(tx)
	}

	// Preserve the legacy event type for blocks edges so existing
	// dependency_added assertions keep passing; other types emit relation_added.
	if relType == task.RelationBlocks {
		if err := s.insertEvent(ctx, tx, taskID, task.EventDependencyAdded, created, relatedID); err != nil {
			return err
		}
	} else {
		message := fmt.Sprintf("%s %s", relType, relatedID)
		if err := s.insertEvent(ctx, tx, taskID, task.EventRelationAdded, created, message); err != nil {
			return err
		}
	}
	return commit(tx)
}
