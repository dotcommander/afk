package runner

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestFirstHeartbeatErrFiltersExpectedCancellation(t *testing.T) {
	t.Parallel()

	errs := make(chan error, 3)
	errs <- context.Canceled
	errs <- context.DeadlineExceeded
	close(errs)
	require.NoError(t, firstHeartbeatErr(errs))

	errs = make(chan error, 1)
	errs <- errors.New("boom")
	close(errs)
	require.Error(t, firstHeartbeatErr(errs))
}

func TestTruncateRunesBoundary(t *testing.T) {
	t.Parallel()

	require.Equal(t, "abc", truncateRunes("abc", 3))
	require.Equal(t, "界…", truncateRunes("界界", 1))
}

func TestBlockPoisonTasksWriterError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.NewSQLite(ctx, store.Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	svc := app.NewService(st, func() time.Time { return time.Now().UTC() })
	require.NoError(t, st.Add(ctx, task.Task{ID: "poison", Created: time.Now().UTC().Format(time.RFC3339), Status: task.StatusPending, Body: "poison"}))
	for range drainPoisonThreshold {
		_, err := svc.PopWithLease(ctx, time.Minute)
		require.NoError(t, err)
		require.NoError(t, svc.Fail(ctx, "poison", "boom"))
		require.NoError(t, st.Update(ctx, "poison", task.EventRequeued, "reset", func(tk *task.Task) bool {
			tk.Reset()
			return true
		}))
	}

	_, err = blockPoisonTasks(ctx, svc, failWriter{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "drain: write")

	var out bytes.Buffer
	n, err := blockPoisonTasks(ctx, svc, &out)
	require.NoError(t, err)
	require.Zero(t, n)
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
