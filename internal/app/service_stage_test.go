package app_test

import (
	"context"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// Tests for the Stage field: AddWithOptions propagation, SetStatusWithStage
// orthogonality (sets-both / leaves-unchanged), and filterTasksByStage.
// See internal/commands/list.go for filterTasksByStage; it lives in package
// commands (internal) so those tests live in internal/commands.

func TestAddWithOptionsPropagatesStage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.AddWithOptions(ctx, task.AddOptions{
		Body:  "staged task",
		Stage: "triage",
	})
	require.NoError(t, err)

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "triage", got.Stage)
}

func TestSetStatusWithStageSetsStatusAndStage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	id, err := svc.Add(ctx, "task to finish with stage")
	require.NoError(t, err)
	// Claim it so Done is a valid transition.
	_, err = svc.Take(ctx, 0, "", "")
	require.NoError(t, err)

	stage := "shipped"
	require.NoError(t, svc.SetStatusWithStage(ctx, id, task.StatusDone, "evidence noted", &stage))

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	// Orthogonality: both status AND stage must be updated in the same call.
	require.Equal(t, task.StatusDone, got.Status, "status must be updated to done")
	require.Equal(t, "shipped", got.Stage, "stage must be set to 'shipped'")
}

func TestSetStatusWithStageNilStagePreservesExistingStage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService(t)

	// Create a task that already has a stage.
	id, err := svc.AddWithOptions(ctx, task.AddOptions{
		Body:  "task with pre-existing stage",
		Stage: "in-progress",
	})
	require.NoError(t, err)
	// Claim it so Done is a valid transition.
	_, err = svc.Take(ctx, 0, "", "")
	require.NoError(t, err)

	// Transition status but pass nil stage (Flags().Changed contract: flag not supplied).
	require.NoError(t, svc.SetStatusWithStage(ctx, id, task.StatusDone, "stage preserved", nil))

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	// Orthogonality: status changes, but the pre-existing stage must NOT be clobbered.
	require.Equal(t, task.StatusDone, got.Status, "status must be updated to done")
	require.Equal(t, "in-progress", got.Stage, "nil stage must leave the existing stage unchanged")
}
