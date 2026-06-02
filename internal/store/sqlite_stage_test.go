package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// fixedTime is a stable timestamp used in store-level stage tests so that
// MarkDone and similar mutators produce deterministic Finished values.
var fixedTime = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

func TestStageRoundTripNonEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "s1", Status: task.StatusTodo, Body: "staged", Stage: "triage"}))

	got, err := s.Get(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "triage", got.Stage)
}

func TestStageRoundTripEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "s2", Status: task.StatusTodo, Body: "no stage"}))

	got, err := s.Get(ctx, "s2")
	require.NoError(t, err)
	require.Equal(t, "", got.Stage)
}

func TestStagePersistsAcrossStatusTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Add(ctx, task.Task{ID: "s3", Status: task.StatusTodo, Body: "transition", Stage: "review"}))

	// Mutate status; stage must survive unchanged.
	require.NoError(t, s.Update(ctx, "s3", task.EventDone, "", func(tk *task.Task) bool {
		return tk.MarkDone(fixedTime)
	}))

	got, err := s.Get(ctx, "s3")
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)
	require.Equal(t, "review", got.Stage)
}
