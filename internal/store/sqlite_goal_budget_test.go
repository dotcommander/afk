package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestCreateGoalRollsBackEveryArtifact(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	created := "2026-07-13T00:00:00Z"
	err := s.CreateGoal(ctx, task.GoalGroup{ID: "g", GroupID: "g", Status: "active", CreatedAt: created}, []task.Task{
		{ID: "duplicate", GroupID: "g", Status: task.StatusTodo, Body: "one", Created: created},
		{ID: "duplicate", GroupID: "g", Status: task.StatusTodo, Body: "two", Created: created},
	})
	require.Error(t, err)
	_, err = s.GetGoalGroup(ctx, "g")
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.Get(ctx, "duplicate")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestGoalIterationLimitPersistsAndResumeRequeues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: path})
	require.NoError(t, err)
	created := "2026-07-13T00:00:00Z"
	require.NoError(t, s.CreateGoal(ctx, task.GoalGroup{ID: "g", GroupID: "g", Status: "active", CreatedAt: created, MaxIterations: 1}, []task.Task{
		{ID: "one", GroupID: "g", Status: task.StatusTodo, Body: "one", Created: created},
		{ID: "two", GroupID: "g", Status: task.StatusTodo, Body: "two", Created: created},
	}))
	now := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	claimed, err := s.ClaimNextForWorker(ctx, now, time.Time{}, "worker", "agent")
	require.NoError(t, err)
	require.Equal(t, "one", claimed.ID)
	_, attemptID, limited, err := s.PrepareGoalInvocation(ctx, claimed.ID, now)
	require.NoError(t, err)
	require.False(t, limited)
	result, err := s.FinalizeGoalInvocation(ctx, store.GoalFinalization{TaskID: claimed.ID, AttemptID: attemptID, Succeeded: true, TokensAvailable: true, Now: now.Add(time.Second)})
	require.NoError(t, err)
	require.True(t, result.Limited)
	require.Equal(t, store.LimitReasonIterations, result.Goal.LimitReason)
	replay, err := s.FinalizeGoalInvocation(ctx, store.GoalFinalization{TaskID: claimed.ID, AttemptID: attemptID, Succeeded: true, TokensAvailable: true, Now: now.Add(2 * time.Second)})
	require.NoError(t, err)
	require.True(t, replay.Replay)
	require.NoError(t, s.Close())

	s, err = store.NewSQLite(ctx, store.Paths{SQLitePath: path})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	goal, err := s.GetGoalGroup(ctx, "g")
	require.NoError(t, err)
	require.Equal(t, int64(1), goal.IterationsUsed)
	require.Equal(t, "budget-limited", goal.Status)
	newLimit := int64(2)
	resumed, err := s.ResumeGoal(ctx, "g", store.GoalResumeChanges{MaxIterations: &newLimit}, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, resumed.ResumedTasks)
	require.Equal(t, "active", resumed.Goal.Status)
	two, err := s.Get(ctx, "two")
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, two.Status)
}

func TestGoalTokenUsageUnavailableFailsClosed(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	created := now.Format(time.RFC3339)
	require.NoError(t, s.CreateGoal(ctx, task.GoalGroup{ID: "g", GroupID: "g", Status: "active", CreatedAt: created, MaxTokens: 100, TokenRegex: `tokens=(\d+)`}, []task.Task{
		{ID: "one", GroupID: "g", Status: task.StatusTodo, Body: "one", Created: created},
		{ID: "two", GroupID: "g", Status: task.StatusTodo, Body: "two", Created: created},
	}))
	claimed, err := s.ClaimNextForWorker(ctx, now, time.Time{}, "worker", "agent")
	require.NoError(t, err)
	_, attemptID, _, err := s.PrepareGoalInvocation(ctx, claimed.ID, now)
	require.NoError(t, err)
	result, err := s.FinalizeGoalInvocation(ctx, store.GoalFinalization{TaskID: claimed.ID, AttemptID: attemptID, Succeeded: true, TokensAvailable: false, Now: now.Add(time.Second)})
	require.NoError(t, err)
	require.True(t, result.Limited)
	require.Equal(t, store.LimitReasonUsageUnavailable, result.Goal.LimitReason)
	two, err := s.Get(ctx, "two")
	require.NoError(t, err)
	require.Equal(t, task.StatusBudgetLimited, two.Status)
	require.Equal(t, store.LimitReasonUsageUnavailable, two.Error)
}

func TestGoalTokenLimitAccountsParsedUsage(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	created := now.Format(time.RFC3339)
	require.NoError(t, s.CreateGoal(ctx, task.GoalGroup{ID: "g", GroupID: "g", Status: "active", CreatedAt: created, MaxTokens: 10, TokenRegex: `tokens=(\d+)`}, []task.Task{
		{ID: "one", GroupID: "g", Status: task.StatusTodo, Body: "one", Created: created},
		{ID: "two", GroupID: "g", Status: task.StatusTodo, Body: "two", Created: created},
	}))
	claimed, err := s.ClaimNextForWorker(ctx, now, time.Time{}, "worker", "agent")
	require.NoError(t, err)
	_, attemptID, _, err := s.PrepareGoalInvocation(ctx, claimed.ID, now)
	require.NoError(t, err)
	result, err := s.FinalizeGoalInvocation(ctx, store.GoalFinalization{TaskID: claimed.ID, AttemptID: attemptID, Succeeded: true, TokensUsed: 10, TokensAvailable: true, Now: now.Add(time.Second)})
	require.NoError(t, err)
	require.True(t, result.Limited)
	require.Equal(t, int64(10), result.Goal.TokensUsed)
	require.Equal(t, store.LimitReasonTokens, result.Goal.LimitReason)
}

func TestGoalDurationPreflightLimitsClaimBeforeInvocation(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	created := now.Add(-2 * time.Hour).Format(time.RFC3339)
	require.NoError(t, s.CreateGoal(ctx, task.GoalGroup{
		ID: "g", GroupID: "g", Status: "active", CreatedAt: created,
		MaxDuration: time.Minute, BudgetEpochStarted: now.Add(-2 * time.Minute).Format(time.RFC3339),
	}, []task.Task{
		{ID: "one", GroupID: "g", Status: task.StatusTodo, Body: "one", Created: created},
		{ID: "two", GroupID: "g", Status: task.StatusTodo, Body: "two", Created: created},
	}))
	claimed, err := s.ClaimNextForWorker(ctx, now, time.Time{}, "worker", "agent")
	require.NoError(t, err)
	goal, _, limited, err := s.PrepareGoalInvocation(ctx, claimed.ID, now)
	require.NoError(t, err)
	require.True(t, limited)
	require.Equal(t, store.LimitReasonDuration, goal.LimitReason)
	for _, id := range []string{"one", "two"} {
		member, getErr := s.Get(ctx, id)
		require.NoError(t, getErr)
		require.Equal(t, task.StatusBudgetLimited, member.Status)
		require.Empty(t, member.LeaseExpires)
		require.NotEmpty(t, member.Finished)
	}
	attempts, err := s.Attempts(ctx, "one")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, task.StatusBudgetLimited, attempts[0].Status)
	require.NotEmpty(t, attempts[0].Finished)
}

func TestGoalDurationEpochPreservesSubsecondPrecision(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 1, 0, 0, 900*int(time.Millisecond), time.UTC)
	created := now.Add(-time.Minute).Format(time.RFC3339Nano)
	require.NoError(t, s.CreateGoal(ctx, task.GoalGroup{
		ID: "g", GroupID: "g", Status: "active", CreatedAt: created, MaxDuration: 100 * time.Millisecond,
	}, []task.Task{{ID: "one", GroupID: "g", Status: task.StatusTodo, Body: "one", Created: created}}))
	claimed, err := s.ClaimNextForWorker(ctx, now, time.Time{}, "worker", "agent")
	require.NoError(t, err)
	goal, _, limited, err := s.PrepareGoalInvocation(ctx, claimed.ID, now)
	require.NoError(t, err)
	require.False(t, limited)
	require.Equal(t, now.Format(time.RFC3339Nano), goal.BudgetEpochStarted)
}

func TestGoalStaleFinalizationCannotOverwriteResumedAttempt(t *testing.T) {
	t.Parallel()
	s := newRetirementStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	created := now.Add(-time.Minute).Format(time.RFC3339Nano)
	require.NoError(t, s.CreateGoal(ctx, task.GoalGroup{
		ID: "g", GroupID: "g", Status: "active", CreatedAt: created,
		MaxDuration: time.Second, BudgetEpochStarted: now.Add(-time.Minute).Format(time.RFC3339Nano),
	}, []task.Task{{ID: "one", GroupID: "g", Status: task.StatusTodo, Body: "one", Created: created}}))

	claimed, err := s.ClaimNextForWorker(ctx, now, time.Time{}, "old-worker", "agent")
	require.NoError(t, err)
	_, oldAttemptID, limited, err := s.PrepareGoalInvocation(ctx, claimed.ID, now)
	require.NoError(t, err)
	require.True(t, limited)

	newDuration := time.Hour
	resumed, err := s.ResumeGoal(ctx, "g", store.GoalResumeChanges{MaxDuration: &newDuration}, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 1, resumed.ResumedTasks)

	claimed, err = s.ClaimNextForWorker(ctx, now.Add(2*time.Second), time.Time{}, "new-worker", "agent")
	require.NoError(t, err)
	_, newAttemptID, limited, err := s.PrepareGoalInvocation(ctx, claimed.ID, now.Add(2*time.Second))
	require.NoError(t, err)
	require.False(t, limited)
	require.NotEqual(t, oldAttemptID, newAttemptID)

	stale, err := s.FinalizeGoalInvocation(ctx, store.GoalFinalization{
		TaskID: claimed.ID, AttemptID: oldAttemptID, Succeeded: true, TokensAvailable: true, Now: now.Add(3 * time.Second),
	})
	require.NoError(t, err)
	require.True(t, stale.Replay)
	member, err := s.Get(ctx, claimed.ID)
	require.NoError(t, err)
	require.Equal(t, task.StatusDoing, member.Status)
	goal, err := s.GetGoalGroup(ctx, "g")
	require.NoError(t, err)
	require.Zero(t, goal.IterationsUsed)
	attempts, err := s.Attempts(ctx, claimed.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.Empty(t, attempts[1].Finished)

	finalized, err := s.FinalizeGoalInvocation(ctx, store.GoalFinalization{
		TaskID: claimed.ID, AttemptID: newAttemptID, Succeeded: true, TokensAvailable: true, Now: now.Add(4 * time.Second),
	})
	require.NoError(t, err)
	require.False(t, finalized.Replay)
	require.Equal(t, int64(1), finalized.Goal.IterationsUsed)
}
