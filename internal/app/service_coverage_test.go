package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestServiceBlocked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	// Add prerequisite and target tasks
	prereqID, err := svc.Add(ctx, "prerequisite task")
	require.NoError(t, err)

	targetID, err := svc.Add(ctx, "blocked target task")
	require.NoError(t, err)

	// Block target on prereq
	err = svc.AddDependency(ctx, targetID, prereqID)
	require.NoError(t, err)

	blocked, err := svc.Blocked(ctx)
	require.NoError(t, err)
	require.Len(t, blocked, 1)
	require.Equal(t, targetID, blocked[0].Task.ID)

	// Complete prerequisite task and re-check blocked list
	require.NoError(t, svc.SetStatusWithStageWorker(ctx, prereqID, task.StatusDone, "done", nil, ""))
	blockedAfterDone, err := svc.Blocked(ctx)
	require.NoError(t, err)
	require.Empty(t, blockedAfterDone)
}

func TestServiceRelationsAndGates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	t1, err := svc.Add(ctx, "task 1")
	require.NoError(t, err)
	t2, err := svc.Add(ctx, "task 2")
	require.NoError(t, err)

	// Test AddRelation
	require.NoError(t, svc.AddRelation(ctx, t1, t2, task.RelationRelates))

	// Test AddGate, Gates, SatisfyGate
	require.NoError(t, svc.AddGate(ctx, t1, "approval"))
	gates, err := svc.Gates(ctx, t1)
	require.NoError(t, err)
	require.Len(t, gates, 1)
	require.Equal(t, "approval", gates[0].Name)
	require.False(t, gates[0].Satisfied)

	require.NoError(t, svc.SatisfyGate(ctx, t1, "approval"))
	gatesAfter, err := svc.Gates(ctx, t1)
	require.NoError(t, err)
	require.True(t, gatesAfter[0].Satisfied)
}

func TestServiceExplainNotReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	_, err := svc.Add(ctx, "task ready")
	require.NoError(t, err)

	explanation, err := svc.ExplainNotReady(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, explanation.TodoTotal)
	require.Equal(t, 1, explanation.Ready)
	require.Equal(t, 0, explanation.Blocked)
}

func TestServiceCheckpointsAndArtifacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	taskID, err := svc.Add(ctx, "checkpoint task")
	require.NoError(t, err)

	// Test Checkpoints
	cpInput := task.Checkpoint{
		TaskID:    taskID,
		Kind:      "progress",
		Key:       "step-1",
		ValueJSON: `{"status":"ok"}`,
	}
	addedCP, err := svc.AddCheckpoint(ctx, cpInput)
	require.NoError(t, err)
	require.Equal(t, "step-1", addedCP.Key)
	require.Equal(t, "afk-cli", addedCP.Provenance.System)

	checkpoints, err := svc.Checkpoints(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, checkpoints, 1)
	require.Equal(t, "step-1", checkpoints[0].Key)

	// Test Artifacts
	artInput := task.Artifact{
		TaskID: taskID,
		Path:   "/tmp/artifact.txt",
	}
	err = svc.AddArtifact(ctx, artInput)
	require.NoError(t, err)

	artifacts, err := svc.Artifacts(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, "/tmp/artifact.txt", artifacts[0].Path)
	require.Equal(t, "afk-cli", artifacts[0].Provenance.System)
}

func TestServiceTakeTaskAndRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	nowFn := func() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) }

	dir := t.TempDir()
	s, err := store.NewSQLite(ctx, store.Paths{SQLitePath: filepath.Join(dir, "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	s.SetClock(nowFn)
	svc := app.NewService(s, nowFn)

	taskID, err := svc.Add(ctx, "take task")
	require.NoError(t, err)

	// Add gate and satisfy it on claim
	require.NoError(t, svc.AddGate(ctx, taskID, "review"))

	claimed, err := svc.TakeTask(ctx, taskID, 10*time.Minute, "worker-1", "agent-1", []string{"review"})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, task.StatusDoing, claimed.Status)

	// Mark failed with worker identity
	require.NoError(t, svc.SetStatusWithStageWorker(ctx, taskID, task.StatusFailed, "something failed", nil, "worker-1"))

	// Test immediate manual retry
	retried, err := svc.Retry(ctx, taskID, task.RetryDispositionManual, "", "retrying now")
	require.NoError(t, err)
	require.Equal(t, task.StatusDoing, retried.Status)

	// Fail again and test deferred retry
	require.NoError(t, svc.SetStatusWithStageWorker(ctx, taskID, task.StatusFailed, "failed again", nil, ""))
	futureTime := "2025-01-01T14:00:00Z"
	deferred, err := svc.Retry(ctx, taskID, task.RetryDispositionDeferred, futureTime, "deferred retry")
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, deferred.Status)
	require.Equal(t, futureTime, deferred.AvailableAt)

	// Verify error when deferred retry timestamp is in the past
	pastTime := "2025-01-01T10:00:00Z"
	require.NoError(t, svc.SetStatusWithStageForce(ctx, taskID, task.StatusFailed, "fail", nil))
	_, err = svc.Retry(ctx, taskID, task.RetryDispositionDeferred, pastTime, "invalid past deferred")
	require.ErrorIs(t, err, task.ErrDeferredRetryNotFuture)
}

func TestServiceSetStatusWithRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	taskID, err := svc.Add(ctx, "request ledger task")
	require.NoError(t, err)

	// Perform mutation with request ID
	reqID := "req-unique-123"
	updated, replayed, err := svc.SetStatusWithRequest(ctx, "actor-1", reqID, taskID, task.StatusDoing, "", nil, false)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, task.StatusDoing, updated.Status)

	// Repeat same request ID (idempotent replay)
	updatedReplay, replayed2, err := svc.SetStatusWithRequest(ctx, "actor-1", reqID, taskID, task.StatusDoing, "", nil, false)
	require.NoError(t, err)
	require.True(t, replayed2)
	require.Equal(t, updated.Status, updatedReplay.Status)
}

func TestServiceResumeGoal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	// Create goal group directly in store
	goalID := "goal-resume-1"
	contract := app.GoalContract{
		Outcome: "test outcome",
		Tasks:   []string{"goal task 1"},
	}
	cwd, err := os.Getwd()
	require.NoError(t, err)

	cfg := app.GoalConfig{MaxTokens: 1000, TokenRegex: `(\d+)`}
	err = svc.CreateGoal(ctx, goalID, "test goal objective", contract, cwd, cfg)
	require.NoError(t, err)

	// Resume goal with new budget override
	newTokens := int64(5000)
	res, err := svc.ResumeGoal(ctx, goalID, store.GoalResumeChanges{
		MaxTokens: &newTokens,
	})
	require.NoError(t, err)
	require.Equal(t, int64(5000), res.Goal.MaxTokens)
}

func TestLoopConfigLoading(t *testing.T) {
	t.Parallel()
	cfg := app.LoadLoopConfig()
	require.NotEmpty(t, cfg.PromptTemplate)
}
