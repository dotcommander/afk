package store

import (
	"context"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// Prune physically removes all tasks with a listed status and returns the number deleted.
// The public CLI prefers setting status=deleted so history stays inspectable.
func (s *SQLiteStore) Prune(ctx context.Context, statuses []task.Status) (int, error) {
	total := 0
	for _, status := range statuses {
		if !task.ValidStatus(status) {
			return total, task.ErrInvalidStatus
		}
		n, err := s.pruneStatus(ctx, status)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// pruneStatus deletes every task with status and records a Pruned event for
// each, all in one transaction so a crash cannot lose the audit events.
func (s *SQLiteStore) pruneStatus(ctx context.Context, status task.Status) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin prune %s: %w", status, err)
	}
	defer rollback(tx)

	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE status = ?`, string(status))
	if err != nil {
		return 0, fmt.Errorf("store: list prune %s: %w", status, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("store: scan prune id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("store: close prune ids: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: prune ids: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE status = ?`, string(status)); err != nil {
		return 0, fmt.Errorf("store: prune %s: %w", status, err)
	}
	for _, id := range ids {
		if err := s.insertEvent(ctx, tx, id, task.EventPruned, "", string(status)); err != nil {
			return 0, err
		}
	}
	return len(ids), commit(tx)
}
