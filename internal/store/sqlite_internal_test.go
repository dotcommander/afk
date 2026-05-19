package store

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteErrorHelpers(t *testing.T) {
	t.Parallel()

	require.False(t, isSQLiteBusy(nil))
	require.True(t, isSQLiteBusy(errors.New("SQLITE_BUSY")))
	require.True(t, isSQLiteBusy(errors.New("database is locked")))
	require.False(t, isSQLiteBusy(errors.New("other sqlite error")))

	require.True(t, isDuplicateTaskID(errors.New("UNIQUE constraint failed: tasks.id")))
	require.False(t, isDuplicateTaskID(errors.New("UNIQUE constraint failed: other.id")))
}

func TestDecodeTagsFallbacks(t *testing.T) {
	t.Parallel()

	require.Nil(t, decodeTags(""))
	require.Nil(t, decodeTags("{not json"))
	require.Equal(t, []string{"a", "b"}, decodeTags(`["a","b"]`))
}

func TestWaitSQLiteBusyRetryHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitSQLiteBusyRetry(ctx), context.Canceled)
}
