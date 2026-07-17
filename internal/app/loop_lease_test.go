package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
	sqlite3 "modernc.org/sqlite/lib"
)

type loopHeartbeatStore struct {
	Store
	heartbeat func() error
}

func (s *loopHeartbeatStore) Heartbeat(context.Context, string, string, time.Time, time.Time) error {
	return s.heartbeat()
}

type codedHeartbeatError struct {
	code int
}

func (e codedHeartbeatError) Error() string { return fmt.Sprintf("sqlite code %d", e.code) }
func (e codedHeartbeatError) Code() int     { return e.code }

func TestHeartbeatMonitorCancelsExecutionOnOwnershipLoss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executionCtx, cancelExecution := context.WithCancel(t.Context())
		defer cancelExecution()
		svc := NewService(&loopHeartbeatStore{
			heartbeat: func() error { return store.ErrWorkerMismatch },
		}, time.Now)

		monitor := svc.startHeartbeat(t.Context(), LoopConfig{
			HeartbeatInterval: time.Second,
			Lease:             time.Minute,
		}, "task-1", "worker-1", time.Now().Add(time.Minute).Format(time.RFC3339), cancelExecution)

		<-executionCtx.Done()
		err := monitor.stop()
		require.ErrorIs(t, err, ErrLeaseLost)
		require.ErrorIs(t, err, store.ErrWorkerMismatch)
	})
}

func TestHeartbeatMonitorToleratesTransientSQLiteBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executionCtx, cancelExecution := context.WithCancel(t.Context())
		defer cancelExecution()
		renewed := make(chan struct{})
		calls := 0
		svc := NewService(&loopHeartbeatStore{
			heartbeat: func() error {
				calls++
				if calls == 1 {
					return fmt.Errorf("heartbeat contention: %w", codedHeartbeatError{code: sqlite3.SQLITE_BUSY})
				}
				close(renewed)
				return nil
			},
		}, time.Now)

		monitor := svc.startHeartbeat(t.Context(), LoopConfig{
			HeartbeatInterval: time.Second,
			Lease:             time.Minute,
		}, "task-1", "worker-1", time.Now().Add(time.Minute).Format(time.RFC3339), cancelExecution)

		<-renewed
		require.NoError(t, executionCtx.Err())
		require.NoError(t, monitor.stop())
		require.Equal(t, 2, calls)
	})
}

func TestHeartbeatMonitorCancelsAtLeaseExpiryWhenRenewalDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executionCtx, cancelExecution := context.WithCancel(t.Context())
		defer cancelExecution()
		svc := NewService(&loopHeartbeatStore{
			heartbeat: func() error {
				t.Fatal("heartbeat called while renewal is disabled")
				return nil
			},
		}, time.Now)

		monitor := svc.startHeartbeat(t.Context(), LoopConfig{
			HeartbeatInterval: 0,
			Lease:             time.Minute,
		}, "task-1", "worker-1", time.Now().Add(time.Minute).Format(time.RFC3339), cancelExecution)

		<-executionCtx.Done()
		err := monitor.stop()
		require.ErrorIs(t, err, ErrLeaseLost)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestSQLiteContentionRecognizesExtendedCodes(t *testing.T) {
	t.Parallel()
	require.True(t, isSQLiteContention(codedHeartbeatError{code: sqlite3.SQLITE_BUSY | 2<<8}))
	require.True(t, isSQLiteContention(codedHeartbeatError{code: sqlite3.SQLITE_LOCKED | 1<<8}))
}

type loopFencedStore struct {
	Store
	worker string
	status task.Status
}

func (s *loopFencedStore) UpdateFencedTask(_ context.Context, _ string, worker string, _ task.EventType, _ string, mutate func(*task.Task) bool) (task.Task, error) {
	t := task.Task{Status: task.StatusDoing}
	if !mutate(&t) {
		return task.Task{}, errors.New("expected terminal mutation")
	}
	s.worker = worker
	s.status = t.Status
	return t, nil
}

func TestClassifyRunFinalizesWithClaimingWorkerFence(t *testing.T) {
	t.Parallel()
	st := &loopFencedStore{}
	svc := NewService(st, time.Now)

	classification, err := svc.classifyRun(context.Background(), "task-1", "worker-1", nil)

	require.NoError(t, err)
	require.Equal(t, "done", classification.status)
	require.Equal(t, "worker-1", st.worker)
	require.Equal(t, task.StatusDone, st.status)
}
