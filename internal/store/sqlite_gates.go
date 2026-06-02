package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// AddGate records a named boolean precondition on a task. It is idempotent:
// re-adding an existing gate is a no-op (the satisfied state is preserved).
func (s *SQLiteStore) AddGate(ctx context.Context, taskID, name string) error {
	created := s.nowString()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO task_gates (task_id, name, satisfied, created)
VALUES (?, ?, 0, ?)
ON CONFLICT(task_id, name) DO NOTHING`, taskID, name, created); err != nil {
		return fmt.Errorf("store: add gate %q on task %s: %w", name, taskID, err)
	}
	return nil
}

// SatisfyGate marks a gate satisfied. An unknown gate returns ErrGateNotFound.
// Gates are kept after satisfaction; there is no inverse operation.
func (s *SQLiteStore) SatisfyGate(ctx context.Context, taskID, name string) error {
	satisfiedAt := s.nowString()
	res, err := s.db.ExecContext(ctx, `
UPDATE task_gates
SET satisfied = 1, satisfied_at = ?
WHERE task_id = ? AND name = ?`, satisfiedAt, taskID, name)
	if err != nil {
		return fmt.Errorf("store: satisfy gate %q on task %s: %w", name, taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: satisfy gate rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("gate %q on task %s: %w", name, taskID, task.ErrGateNotFound)
	}
	return nil
}

// Gates returns all gates for a task, ordered by name.
func (s *SQLiteStore) Gates(ctx context.Context, taskID string) ([]task.Gate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, name, satisfied, created, satisfied_at
FROM task_gates
WHERE task_id = ?
ORDER BY name`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: gates: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked below

	var gates []task.Gate
	for rows.Next() {
		gate, err := scanGate(rows)
		if err != nil {
			return nil, err
		}
		gates = append(gates, gate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: gate rows: %w", err)
	}
	return gates, nil
}

func scanGate(rows *sql.Rows) (task.Gate, error) {
	var (
		gate        task.Gate
		satisfied   int
		created     string
		satisfiedAt sql.NullString
	)
	if err := rows.Scan(&gate.TaskID, &gate.Name, &satisfied, &created, &satisfiedAt); err != nil {
		return task.Gate{}, fmt.Errorf("store: scan gate: %w", err)
	}
	gate.Satisfied = satisfied != 0
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return task.Gate{}, fmt.Errorf("store: parse gate created %q: %w", created, err)
	}
	gate.CreatedAt = createdAt
	if satisfiedAt.Valid && satisfiedAt.String != "" {
		at, err := time.Parse(time.RFC3339, satisfiedAt.String)
		if err != nil {
			return task.Gate{}, fmt.Errorf("store: parse gate satisfied_at %q: %w", satisfiedAt.String, err)
		}
		gate.SatisfiedAt = &at
	}
	return gate, nil
}
