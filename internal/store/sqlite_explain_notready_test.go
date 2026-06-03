package store_test

import (
	"context"
	"testing"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestExplainNotReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("empty queue reports zero todo", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		e, err := s.ExplainNotReady(ctx)
		require.NoError(t, err)
		require.Equal(t, store.NotReadyExplanation{TodoTotal: 0, Ready: 0, Blocked: 0}, e)
	})

	t.Run("dependency blocked todo counts as blocked", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		require.NoError(t, s.Add(ctx, task.Task{ID: "prereq", Status: task.StatusTodo, Body: "prereq"}))
		require.NoError(t, s.Add(ctx, task.Task{ID: "dependent", Status: task.StatusTodo, Body: "dependent"}))
		require.NoError(t, s.AddDependency(ctx, "dependent", "prereq"))

		e, err := s.ExplainNotReady(ctx)
		require.NoError(t, err)
		// prereq is ready; dependent is blocked by the unfinished prereq.
		require.Equal(t, 2, e.TodoTotal)
		require.Equal(t, 1, e.Ready)
		require.Equal(t, 1, e.Blocked)
		require.Equal(t, e.TodoTotal, e.Ready+e.Blocked)
	})

	t.Run("resource lock blocks a sibling todo", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		// Active claim on a resource key.
		require.NoError(t, s.Add(ctx, task.Task{ID: "active", Status: task.StatusDoing, Body: "active", ResourceKey: "lock-A"}))
		// Todo task contending for the same resource key -> blocked.
		require.NoError(t, s.Add(ctx, task.Task{ID: "waiting", Status: task.StatusTodo, Body: "waiting", ResourceKey: "lock-A"}))
		// Todo task with no contention -> ready.
		require.NoError(t, s.Add(ctx, task.Task{ID: "free", Status: task.StatusTodo, Body: "free"}))

		e, err := s.ExplainNotReady(ctx)
		require.NoError(t, err)
		require.Equal(t, 2, e.TodoTotal) // only todo tasks counted
		require.Equal(t, 1, e.Ready)     // "free"
		require.Equal(t, 1, e.Blocked)   // "waiting"
	})
}
