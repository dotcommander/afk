package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func TestDecodeInputEmptyAndTooLarge(t *testing.T) {
	t.Parallel()

	in, err := decodeSetTask(httptest.NewRequest(http.MethodPatch, "/api/tasks/1", nil))
	require.NoError(t, err)
	require.Equal(t, setTaskInput{}, in)

	_, err = decodeSetTask(httptest.NewRequest(http.MethodPatch, "/api/tasks/1", strings.NewReader(strings.Repeat("x", 70*1024))))
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode body")
}

func TestDecodeInputStrictJSON(t *testing.T) {
	t.Parallel()

	_, err := decodeSetTask(httptest.NewRequest(http.MethodPatch, "/api/tasks/1", strings.NewReader(`{"status":"done","extra":true}`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown field "extra"`)

	_, err = decodeSetTask(httptest.NewRequest(http.MethodPatch, "/api/tasks/1", strings.NewReader(`{"status":"done"} {"status":"failed"}`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one JSON value")
}

func TestSmallHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, "fallback", defaultString("", "fallback"))
	require.Equal(t, "value", defaultString("value", "fallback"))
	lease, err := parseLease("")
	require.NoError(t, err)
	require.Zero(t, lease)
	lease, err = parseLease("2m")
	require.NoError(t, err)
	require.Equal(t, 2*time.Minute, lease)
	_, err = parseLease("-1s")
	require.Error(t, err)
	require.Contains(t, err.Error(), "duration must be positive")
	require.Len(t, newCSRFToken(), 43)
}

func TestWriteResultErrorMapping(t *testing.T) {
	t.Parallel()

	notFound := httptest.NewRecorder()
	writeResult(notFound, nil, app.ErrNotFound)
	require.Equal(t, http.StatusNotFound, notFound.Code)

	internal := httptest.NewRecorder()
	writeResult(internal, nil, errors.New("boom"))
	require.Equal(t, http.StatusInternalServerError, internal.Code)
}

func TestResolveIDMissing(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	id, ok := resolveID(rec, httptest.NewRequest(http.MethodGet, "/api/tasks/", nil))
	require.False(t, ok)
	require.Empty(t, id)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandlersPropagateServiceErrors(t *testing.T) {
	t.Parallel()

	svc := app.NewService(&failingStore{err: errors.New("boom")}, time.Now)
	s := New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), "127.0.0.1:0", false)
	h := s.Handler()

	for _, path := range []string{"/api/status", "/api/tasks", "/api/tasks?q=x", "/api/paths"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusInternalServerError, rec.Code, path)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/take", nil)
	req.Header.Set(csrfHeader, s.csrfToken)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestServerRunReportsListenError(t *testing.T) {
	t.Parallel()

	svc := app.NewService(&failingStore{}, time.Now)
	s := New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), "not-an-addr", false)
	err := s.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listen")
}

func TestBrowserCommandSelection(t *testing.T) {
	t.Parallel()

	name, args := browserCommand("darwin", "http://127.0.0.1/")
	require.Equal(t, "open", name)
	require.Equal(t, []string{"http://127.0.0.1/"}, args)

	name, args = browserCommand("windows", "http://127.0.0.1/")
	require.Equal(t, "cmd", name)
	require.Equal(t, []string{"/c", "start", "http://127.0.0.1/"}, args)

	name, args = browserCommand("linux", "http://127.0.0.1/")
	require.Equal(t, "xdg-open", name)
	require.Equal(t, []string{"http://127.0.0.1/"}, args)
}

type failingStore struct {
	app.Store
	err error
}

func (s *failingStore) List(context.Context) ([]task.Task, error) { return nil, s.err }
func (s *failingStore) Counts(context.Context) (map[task.Status]int, error) {
	return nil, s.err
}
func (s *failingStore) ActiveLists(context.Context) ([]task.Task, []task.Task, error) {
	return nil, nil, s.err
}
func (s *failingStore) Ready(context.Context) ([]task.Task, error) { return nil, s.err }
func (s *failingStore) ClaimNextForWorker(context.Context, time.Time, time.Time, string, string) (*task.Task, error) {
	return nil, s.err
}
func (s *failingStore) RecentDistinctCWDs(context.Context, int) ([]string, error) {
	return nil, s.err
}
