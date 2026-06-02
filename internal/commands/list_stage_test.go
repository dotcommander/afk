package commands

import (
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestFilterTasksByStageMatchesNonEmptyFilter(t *testing.T) {
	t.Parallel()

	tasks := []task.Task{
		{ID: "1", Stage: "triage"},
		{ID: "2", Stage: "review"},
		{ID: "3", Stage: "triage"},
		{ID: "4", Stage: ""},
	}

	got := filterTasksByStage(tasks, "triage")
	require.Len(t, got, 2)
	require.Equal(t, "1", got[0].ID)
	require.Equal(t, "3", got[1].ID)
}

func TestFilterTasksByStageEmptyFilterReturnsAll(t *testing.T) {
	t.Parallel()

	tasks := []task.Task{
		{ID: "1", Stage: "triage"},
		{ID: "2", Stage: ""},
	}

	got := filterTasksByStage(tasks, "")
	// Empty filter must return the exact input slice — no copy, no mutation.
	require.Equal(t, tasks, got)
}

func TestFilterTasksByStageNoMatchReturnsEmpty(t *testing.T) {
	t.Parallel()

	tasks := []task.Task{
		{ID: "1", Stage: "triage"},
	}

	got := filterTasksByStage(tasks, "shipped")
	require.Empty(t, got)
}
