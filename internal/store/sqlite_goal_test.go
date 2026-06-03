package store_test

import (
	"context"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// TestGoalGroups exercises the goal_groups store methods per Verification
// Surface §6. The table and method bodies land in Phase C; these assertions
// encode the contract and may fail (stub methods return "not implemented")
// until then.
func TestGoalGroups(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newStore(t)

	g := task.GoalGroup{
		ID:        "goal-1",
		Objective: "add CSV export to the report command",
		Status:    "active",
		CreatedAt: "2025-01-02T03:04:05Z",
		GroupID:   "goal-1",
	}

	require.NoError(t, s.AddGoalGroup(ctx, g))

	got, err := s.GetGoalGroup(ctx, "goal-1")
	require.NoError(t, err)
	require.Equal(t, g.ID, got.ID)
	require.Equal(t, g.Objective, got.Objective)
	require.Equal(t, "active", got.Status)

	require.NoError(t, s.UpdateGoalGroupStatus(ctx, "goal-1", "complete"))
	got, err = s.GetGoalGroup(ctx, "goal-1")
	require.NoError(t, err)
	require.Equal(t, "complete", got.Status)
}
