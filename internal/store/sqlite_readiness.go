package store

import (
	"context"
	"fmt"

	"github.com/dotcommander/afk/internal/task"
)

// NotReadyExplanation is a structured, formatting-free breakdown of why the
// queue currently has no ready task. Counts are derived from readyWhereSQL so
// the reason taxonomy never drifts from the readiness predicate itself.
type NotReadyExplanation struct {
	TodoTotal int // todo tasks in the queue
	Ready     int // todo tasks that satisfy readyWhereSQL
	Blocked   int // todo tasks that do NOT satisfy readyWhereSQL (time/deps/locks/gates)
}

// ExplainNotReady reports why the queue has no claimable task, deriving every
// count from readyWhereSQL (the single readiness predicate) rather than
// re-deriving dependency/lock/gate logic. Blocked = TodoTotal - Ready.
func (s *SQLiteStore) ExplainNotReady(ctx context.Context) (NotReadyExplanation, error) {
	var e NotReadyExplanation
	if err := s.db.QueryRowContext(ctx, `
SELECT
	(SELECT COUNT(*) FROM tasks WHERE status = ?) AS todo_total,
	(SELECT COUNT(*) FROM tasks WHERE status = ?`+readyWhereSQL+`) AS ready`,
		string(task.StatusTodo),
		string(task.StatusTodo), s.nowString(), string(task.StatusDone), string(task.StatusDoing),
	).Scan(&e.TodoTotal, &e.Ready); err != nil {
		return NotReadyExplanation{}, fmt.Errorf("store: explain not ready: %w", err)
	}
	e.Blocked = e.TodoTotal - e.Ready
	return e, nil
}
