package store_test

import (
	"context"
	"testing"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestListByStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	// Insert in a deliberately non-sorted body order; ordinal is assigned by
	// insertion, so ordinal/rowid order == insertion order here.
	require.NoError(t, s.Add(ctx, task.Task{ID: "a", Status: task.StatusTodo, Body: "first todo"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "b", Status: task.StatusDoing, Body: "a doing"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "c", Status: task.Status("pending"), Body: "legacy pending"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "d", Status: task.StatusDone, Body: "done one"}))
	require.NoError(t, s.Add(ctx, task.Task{ID: "e", Status: task.StatusTodo, Body: "second todo"}))

	// (a) + (b): todo filter returns canonical todo rows AND legacy "pending".
	got, err := s.ListByStatus(ctx, task.StatusTodo)
	require.NoError(t, err)
	gotIDs := make([]string, len(got))
	for i, tk := range got {
		gotIDs[i] = tk.ID
	}
	require.Equal(t, []string{"a", "c", "e"}, gotIDs)

	// (c): order matches the order these rows appear in a full List().
	all, err := s.List(ctx)
	require.NoError(t, err)
	var wantOrder []string
	for _, tk := range all {
		if task.NormalizeStatus(tk.Status) == task.StatusTodo {
			wantOrder = append(wantOrder, tk.ID)
		}
	}
	require.Equal(t, wantOrder, gotIDs)

	// doing filter is scoped and excludes other statuses.
	doing, err := s.ListByStatus(ctx, task.StatusDoing)
	require.NoError(t, err)
	require.Len(t, doing, 1)
	require.Equal(t, "b", doing[0].ID)

	// done filter (no legacy alias) returns just the done row.
	done, err := s.ListByStatus(ctx, task.StatusDone)
	require.NoError(t, err)
	require.Len(t, done, 1)
	require.Equal(t, "d", done[0].ID)
}
