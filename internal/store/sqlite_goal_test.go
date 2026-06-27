package store_test

import (
	"context"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// TestCountTasksByGroupID verifies the GROUP BY query returns per-status counts
// for one group and excludes tasks belonging to a different group.
func TestCountTasksByGroupID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newStore(t)

	// Two tasks in group-A with different statuses, one in group-B.
	require.NoError(t, s.Add(ctx, task.Task{ID: "a1", GroupID: "group-A", Status: task.StatusTodo, Body: "task 1"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "a2", GroupID: "group-A", Status: task.StatusTodo, Body: "task 2"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "a3", GroupID: "group-A", Status: task.StatusDone, Body: "task 3"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "b1", GroupID: "group-B", Status: task.StatusTodo, Body: "other group"}))

	counts, err := s.CountTasksByGroupID(ctx, "group-A")
	require.NoError(t, err)
	require.Equal(t, 2, counts[string(task.StatusTodo)], "expected 2 todo tasks in group-A")
	require.Equal(t, 1, counts[string(task.StatusDone)], "expected 1 done task in group-A")
	require.NotContains(t, counts, string(task.StatusDoing), "no doing tasks in group-A")

	// group-B task must not appear in group-A counts.
	total := 0
	for _, v := range counts {
		total += v
	}
	require.Equal(t, 3, total, "group-A should have exactly 3 tasks total")

	// Empty result for unknown group returns non-nil empty map.
	unknown, err := s.CountTasksByGroupID(ctx, "no-such-group")
	require.NoError(t, err)
	require.NotNil(t, unknown)
	require.Empty(t, unknown)
}

// TestGoalGroups exercises the goal_groups store methods and query contract.
func TestGoalGroups(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newStore(t)

	g := task.GoalGroup{
		ID:        "goal-1",
		Objective: "add CSV export to the report command",
		Outcome:   "report command supports CSV export",
		Status:    "active",
		CreatedAt: "2025-01-02T03:04:05Z",
		GroupID:   "goal-1",
	}

	require.NoError(t, s.AddGoalGroup(ctx, g))

	got, err := s.GetGoalGroup(ctx, "goal-1")
	require.NoError(t, err)
	require.Equal(t, g.ID, got.ID)
	require.Equal(t, g.Objective, got.Objective)
	require.Equal(t, g.Outcome, got.Outcome)
	require.Equal(t, "active", got.Status)

	require.NoError(t, s.UpdateGoalGroupStatus(ctx, "goal-1", "complete"))
	got, err = s.GetGoalGroup(ctx, "goal-1")
	require.NoError(t, err)
	require.Equal(t, "complete", got.Status)
}
