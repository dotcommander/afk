package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// markDone transitions taskID to done via Update, mirroring the worker contract.
func markDone(t *testing.T, s *store.SQLiteStore, ctx context.Context, id string) {
	t.Helper()
	now := time.Now()
	require.NoError(t, s.Update(ctx, id, task.EventDone, "", func(tk *task.Task) bool {
		return tk.MarkDone(now)
	}))
}

// TestRelationBlocks_GatesReadiness verifies that a blocks edge keeps taskID out
// of Ready until its related task is done.
func TestRelationBlocks_GatesReadiness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "a")
	addTask(t, s, ctx, "b")
	require.NoError(t, s.AddRelation(ctx, "a", "b", task.RelationBlocks))

	// "a" is blocked; only "b" is ready.
	require.Equal(t, []string{"b"}, readyIDs(t, s, ctx))

	markDone(t, s, ctx, "b")

	// "b" is done; "a" is now ready.
	require.Equal(t, []string{"a"}, readyIDs(t, s, ctx))
}

// TestRelationRelates_DoesNotBlock verifies that a relates edge is informational:
// the source task is immediately ready regardless of the target's status.
func TestRelationRelates_DoesNotBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "a")
	addTask(t, s, ctx, "b")
	require.NoError(t, s.AddRelation(ctx, "a", "b", task.RelationRelates))

	ids := readyIDs(t, s, ctx)
	require.Contains(t, ids, "a", "task with relates edge must be immediately ready")
}

// TestRelationParent_DoesNotBlock verifies that a parent edge is informational:
// the child task is immediately ready.
func TestRelationParent_DoesNotBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "child")
	addTask(t, s, ctx, "parent")
	require.NoError(t, s.AddRelation(ctx, "child", "parent", task.RelationParent))

	ids := readyIDs(t, s, ctx)
	require.Contains(t, ids, "child", "child with parent edge must be immediately ready")
}

// TestRelationDuplicates_DoesNotBlock verifies that a duplicates edge is
// informational: the source task is immediately ready.
func TestRelationDuplicates_DoesNotBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "a")
	addTask(t, s, ctx, "b")
	require.NoError(t, s.AddRelation(ctx, "a", "b", task.RelationDuplicates))

	ids := readyIDs(t, s, ctx)
	require.Contains(t, ids, "a", "task with duplicates edge must be immediately ready")
}

// TestRelationSelf_Rejected verifies that self-relations are rejected for all
// relation types with errors.Is(err, task.ErrInvalidRelation).
func TestRelationSelf_Rejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, relType := range []task.RelationType{
		task.RelationBlocks,
		task.RelationRelates,
		task.RelationDuplicates,
		task.RelationParent,
	} {
		relType := relType
		t.Run(string(relType), func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			addTask(t, s, ctx, "x")

			err := s.AddRelation(ctx, "x", "x", relType)
			require.Error(t, err)
			require.ErrorIs(t, err, task.ErrInvalidRelation)
		})
	}
}

// TestAddDependency_EqualsBlocksRelation verifies that AddDependency is
// identical in behavior to AddRelation with RelationBlocks: it gates readiness
// and Dependencies returns Type == RelationBlocks.
func TestAddDependency_EqualsBlocksRelation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	addTask(t, s, ctx, "a")
	addTask(t, s, ctx, "b")
	require.NoError(t, s.AddDependency(ctx, "a", "b"))

	// "a" must be blocked by "b" (same gating as RelationBlocks).
	require.Equal(t, []string{"b"}, readyIDs(t, s, ctx))

	markDone(t, s, ctx, "b")
	require.Equal(t, []string{"a"}, readyIDs(t, s, ctx))

	// Dependencies must expose Type == RelationBlocks.
	deps, err := s.Dependencies(ctx, "a")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, task.RelationBlocks, deps[0].Type)
}

// TestRelationType_RoundTrips verifies that after AddRelation with a non-blocks
// type, Dependencies returns the edge with the correct Type field.
func TestRelationType_RoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []task.RelationType{
		task.RelationRelates,
		task.RelationDuplicates,
		task.RelationParent,
	}
	for _, relType := range cases {
		relType := relType
		t.Run(string(relType), func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			addTask(t, s, ctx, "a")
			addTask(t, s, ctx, "b")

			require.NoError(t, s.AddRelation(ctx, "a", "b", relType))

			deps, err := s.Dependencies(ctx, "a")
			require.NoError(t, err)
			require.Len(t, deps, 1)
			require.Equal(t, relType, deps[0].Type)
		})
	}
}

// TestRelationCycle_OnlyForBlocks verifies that cycle detection applies to
// blocks edges (A→B then B→A errors) but NOT to non-blocking edges (same pair
// with RelationRelates must succeed).
func TestRelationCycle_OnlyForBlocks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("blocks cycle is rejected", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		addTask(t, s, ctx, "a")
		addTask(t, s, ctx, "b")

		require.NoError(t, s.AddRelation(ctx, "a", "b", task.RelationBlocks))
		err := s.AddRelation(ctx, "b", "a", task.RelationBlocks)
		require.ErrorIs(t, err, store.ErrDependencyCycle)
	})

	t.Run("relates cycle is allowed", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		addTask(t, s, ctx, "a")
		addTask(t, s, ctx, "b")

		require.NoError(t, s.AddRelation(ctx, "a", "b", task.RelationRelates))
		require.NoError(t, s.AddRelation(ctx, "b", "a", task.RelationRelates),
			"non-blocking reverse edge must not trigger cycle detection")
	})
}
