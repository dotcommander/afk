package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// addTask is a convenience helper that adds a todo task with a given ID.
func addTask(t *testing.T, s interface {
	Add(context.Context, task.Task) error
}, ctx context.Context, id string) {
	t.Helper()
	require.NoError(t, s.Add(ctx, task.Task{ID: id, Status: task.StatusTodo, Body: id}))
}

// readyIDs returns the IDs of tasks returned by Ready(), in order.
func readyIDs(t *testing.T, s interface {
	Ready(context.Context) ([]task.Task, error)
}, ctx context.Context) []string {
	t.Helper()
	tasks, err := s.Ready(ctx)
	require.NoError(t, err)
	ids := make([]string, len(tasks))
	for i, tk := range tasks {
		ids[i] = tk.ID
	}
	return ids
}

func TestGateBaseline_NoGatesIsReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "task-1")

	ids := readyIDs(t, s, ctx)
	require.Equal(t, []string{"task-1"}, ids)
}

func TestGateAddGate_BlocksReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "task-1")
	require.NoError(t, s.AddGate(ctx, "task-1", "approval"))

	ids := readyIDs(t, s, ctx)
	require.Empty(t, ids)
}

func TestGateSatisfyGate_Unblocks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "task-1")
	require.NoError(t, s.AddGate(ctx, "task-1", "approval"))
	require.NoError(t, s.SatisfyGate(ctx, "task-1", "approval"))

	ids := readyIDs(t, s, ctx)
	require.Equal(t, []string{"task-1"}, ids)
}

func TestGateAddGate_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "task-1")
	require.NoError(t, s.AddGate(ctx, "task-1", "approval"))
	require.NoError(t, s.AddGate(ctx, "task-1", "approval")) // second call must not error

	gates, err := s.Gates(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, gates, 1)
	require.Equal(t, "approval", gates[0].Name)
}

func TestGateSatisfyGate_UnknownReturnsErrGateNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "task-1")

	err := s.SatisfyGate(ctx, "task-1", "missing")
	require.True(t, errors.Is(err, task.ErrGateNotFound), "expected ErrGateNotFound, got: %v", err)
}

func TestGateGates_Shape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("unsatisfied gate has nil SatisfiedAt", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		addTask(t, s, ctx, "task-1")
		require.NoError(t, s.AddGate(ctx, "task-1", "review"))

		gates, err := s.Gates(ctx, "task-1")
		require.NoError(t, err)
		require.Len(t, gates, 1)
		require.Equal(t, "review", gates[0].Name)
		require.False(t, gates[0].Satisfied)
		require.Nil(t, gates[0].SatisfiedAt)
	})

	t.Run("satisfied gate has non-nil SatisfiedAt", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		addTask(t, s, ctx, "task-1")
		require.NoError(t, s.AddGate(ctx, "task-1", "review"))
		require.NoError(t, s.SatisfyGate(ctx, "task-1", "review"))

		gates, err := s.Gates(ctx, "task-1")
		require.NoError(t, err)
		require.Len(t, gates, 1)
		require.Equal(t, "review", gates[0].Name)
		require.True(t, gates[0].Satisfied)
		require.NotNil(t, gates[0].SatisfiedAt)
	})
}

func TestGateMultipleGates_AllMustBeSatisfied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("neither satisfied: not ready", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		addTask(t, s, ctx, "task-1")
		require.NoError(t, s.AddGate(ctx, "task-1", "alpha"))
		require.NoError(t, s.AddGate(ctx, "task-1", "beta"))

		require.Empty(t, readyIDs(t, s, ctx))
	})

	t.Run("one satisfied: still not ready", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		addTask(t, s, ctx, "task-1")
		require.NoError(t, s.AddGate(ctx, "task-1", "alpha"))
		require.NoError(t, s.AddGate(ctx, "task-1", "beta"))

		require.NoError(t, s.SatisfyGate(ctx, "task-1", "alpha"))
		require.Empty(t, readyIDs(t, s, ctx))
	})

	t.Run("both satisfied: ready", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		addTask(t, s, ctx, "task-1")
		require.NoError(t, s.AddGate(ctx, "task-1", "alpha"))
		require.NoError(t, s.AddGate(ctx, "task-1", "beta"))

		require.NoError(t, s.SatisfyGate(ctx, "task-1", "alpha"))
		require.NoError(t, s.SatisfyGate(ctx, "task-1", "beta"))
		require.Equal(t, []string{"task-1"}, readyIDs(t, s, ctx))
	})
}
