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
	relType, err := validateRelation(taskID, relatedID, relType)
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
	if err := s.insertRelation(ctx, tx, taskID, relatedID, relType, created); err != nil {
		return err
	}
	if err := s.recordRelationEvent(ctx, tx, taskID, relatedID, relType, prior, created); err != nil {
		return err
	}
	return commit(tx)
}

func (s *SQLiteStore) insertRelation(ctx context.Context, tx *sql.Tx, taskID, relatedID string, relType task.RelationType, created string) error {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO task_dependencies (task_id, depends_on_id, created, relation_type)
VALUES (?, ?, ?, ?)
ON CONFLICT(task_id, depends_on_id) DO UPDATE SET relation_type = excluded.relation_type`,
		taskID, relatedID, created, string(relType)); err != nil {
		return fmt.Errorf("store: add relation %s -> %s: %w", taskID, relatedID, err)
	}
	return nil
}

// validateRelation rejects empty/self relations and normalizes the relation
// type. An empty relType defaults to RelationBlocks (via ParseRelationType).
func validateRelation(taskID, relatedID string, relType task.RelationType) (task.RelationType, error) {
	if taskID == "" || relatedID == "" {
		return "", ErrInvalidDependency
	}
	if taskID == relatedID {
		return "", fmt.Errorf("task %s: cannot relate to itself: %w", taskID, task.ErrInvalidRelation)
	}
	return task.ParseRelationType(string(relType))
}

// recordRelationEvent emits the appropriate edge event, preserving the legacy
// dependency_added event for blocks edges while other types emit relation_added.
// A re-add that does not change the relation type is a no-op: emit no event.
func (s *SQLiteStore) recordRelationEvent(ctx context.Context, tx *sql.Tx, taskID, relatedID string, relType task.RelationType, prior, created string) error {
	if prior == string(relType) {
		return nil
	}
	if relType == task.RelationBlocks {
		return s.insertEvent(ctx, tx, taskID, task.EventDependencyAdded, created, relatedID)
	}
	message := fmt.Sprintf("%s %s", relType, relatedID)
	return s.insertEvent(ctx, tx, taskID, task.EventRelationAdded, created, message)
}

// Dependencies returns the typed relation edges declared on taskID.
func (s *SQLiteStore) Dependencies(ctx context.Context, taskID string) ([]task.Dependency, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, depends_on_id, created, relation_type
FROM task_dependencies
WHERE task_id = ?
ORDER BY created, depends_on_id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: dependencies: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var deps []task.Dependency
	for rows.Next() {
		var dep task.Dependency
		var relType string
		if err := rows.Scan(&dep.TaskID, &dep.DependsOnID, &dep.Created, &relType); err != nil {
			return nil, fmt.Errorf("store: scan dependency: %w", err)
		}
		dep.Type = task.RelationType(relType)
		deps = append(deps, dep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: dependency rows: %w", err)
	}
	return deps, nil
}
