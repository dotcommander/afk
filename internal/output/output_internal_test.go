package output

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestWriteStatusSectionEmptyAndWriterError(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, writeStatusSection(&out, "Todo:", nil, time.Now(), false))
	require.Contains(t, out.String(), "Todo:")
	require.Contains(t, out.String(), "none")

	err := writeStatusSection(errWriterInternal{}, "Todo:", nil, time.Now(), false)
	require.Error(t, err)
	require.True(t, errors.Is(err, errInternalWrite))
}

func TestWriteStatusTextWriterErrorAfterCounts(t *testing.T) {
	t.Parallel()

	w := &countingErrWriter{failAfter: 20}
	err := writeStatusText(w, statusData{tally: map[task.Status]int{}, now: time.Now()})
	require.Error(t, err)
	require.True(t, errors.Is(err, errInternalWrite))
}

func TestJSONAndDetailErrorPaths(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := WriteJSONLine(&out, make(chan int), "bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshal")

	err = WriteJSONLine(errWriterInternal{}, map[string]string{"ok": "true"}, "bad")
	require.Error(t, err)
	require.True(t, errors.Is(err, errInternalWrite))

	err = writeTaskDetail(errWriterInternal{}, task.Task{ID: "1", Body: "body"})
	require.Error(t, err)
	require.True(t, errors.Is(err, errInternalWrite))

	err = writeListTable(errWriterInternal{}, []task.Task{{ID: "1", Body: "body"}}, 0)
	require.Error(t, err)
	require.True(t, errors.Is(err, errInternalWrite))
}

func TestWriteExplainRowsWithoutOptionalFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, writeExplainEvent(&out, task.Event{At: "2025-01-02T03:04:05Z", Type: task.EventClaimed}))
	require.Contains(t, out.String(), "claimed")
	require.NotContains(t, out.String(), "message")

	out.Reset()
	require.NoError(t, writeExplainAttempt(&out, task.Attempt{ID: 7, Status: task.StatusDoing}))
	require.Contains(t, out.String(), "#7")
	require.Contains(t, out.String(), "doing")
}

func TestWriteExplainRowWriterErrors(t *testing.T) {
	t.Parallel()

	err := writeExplainEvent(errWriterInternal{}, task.Event{At: "now", Type: task.EventFailed, Message: "boom"})
	require.Error(t, err)
	require.True(t, errors.Is(err, errInternalWrite))

	err = writeExplainAttempt(errWriterInternal{}, task.Attempt{ID: 1, Status: task.StatusFailed, Started: "now"})
	require.Error(t, err)
	require.True(t, errors.Is(err, errInternalWrite))
}

func TestWriteExplainSectionPropagatesRowError(t *testing.T) {
	t.Parallel()

	err := writeExplainSection(&bytes.Buffer{}, "Events:", 1, 1, "events", func() error {
		return errInternalWrite
	})
	require.ErrorIs(t, err, errInternalWrite)
}

var errInternalWrite = errors.New("write failed")

type errWriterInternal struct{}

func (errWriterInternal) Write([]byte) (int, error) {
	return 0, errInternalWrite
}

type countingErrWriter struct {
	written   int
	failAfter int
}

func (w *countingErrWriter) Write(p []byte) (int, error) {
	if w.written >= w.failAfter {
		return 0, errInternalWrite
	}
	w.written += len(p)
	return len(p), nil
}
