package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"io"
	"log/slog"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/server"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

// newServerFixture opens a temp SQLite store, seeds tasks, and returns a
// ready-to-call http.Handler. It also returns the store for post-call
// assertions. The caller is responsible for no cleanup beyond t.Cleanup
// (registered inside this function).
func newServerFixture(t *testing.T) (http.Handler, *app.Service, *store.SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	queuePath := filepath.Join(t.TempDir(), "tasks.sqlite")
	st, err := store.NewSQLite(ctx, store.Paths{SQLitePath: queuePath})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	now := func() time.Time { return time.Now().UTC() }
	svc := app.NewService(st, now)

	// Seed: two pending tasks + one done task.
	_, err = svc.Add(ctx, "pending one")
	require.NoError(t, err)
	_, err = svc.Add(ctx, "pending two")
	require.NoError(t, err)
	doneID, err := svc.Add(ctx, "done task")
	require.NoError(t, err)
	require.NoError(t, svc.Done(ctx, doneID, ""))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(svc, logger, "127.0.0.1:0", false)
	return srv.Handler(), svc, st
}

func withCSRF(t *testing.T, h http.Handler, req *http.Request) *http.Request {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	prefix := `<meta name="afk-csrf-token" content="`
	page := rec.Body.String()
	start := strings.Index(page, prefix)
	require.NotEqual(t, -1, start)
	after, ok := strings.CutPrefix(page[start:], prefix)
	require.True(t, ok)
	token, _, ok := strings.Cut(after, `">`)
	require.True(t, ok)
	require.NotEmpty(t, token)
	req.Header.Set("X-AFK-CSRF-Token", token)
	return req
}

func TestGETIndexReturns200AndHTML(t *testing.T) {
	t.Parallel()

	h, _, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	ct := rec.Header().Get("Content-Type")
	require.Contains(t, ct, "text/html")
	require.Contains(t, rec.Body.String(), "<html")
}

func TestGETStatusReturnsCountsAndTasks(t *testing.T) {
	t.Parallel()

	h, _, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	counts, ok := body["counts"].(map[string]any)
	require.True(t, ok, "counts field must be a JSON object")
	require.Equal(t, float64(2), counts["pending"])
	require.Equal(t, float64(1), counts["done"])

	_, hasPending := body["pending"]
	_, hasWorking := body["working"]
	require.True(t, hasPending, "status response must include pending list")
	require.True(t, hasWorking, "status response must include working list")
}

func TestGETTasksReturnsAllSeededTasks(t *testing.T) {
	t.Parallel()

	h, _, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var tasks []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tasks))
	require.Len(t, tasks, 3, "all three seeded tasks must be returned")
}

func TestGETTasksStatusFilterReturnsPendingOnly(t *testing.T) {
	t.Parallel()

	h, _, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks?status=pending", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var tasks []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tasks))
	require.Len(t, tasks, 2, "status=pending must return only the two pending tasks")
	for _, tk := range tasks {
		require.Equal(t, "pending", tk["status"])
	}
}

func TestGETTaskByIDReturnsExplainData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc, _ := newServerFixture(t)

	// Grab a known pending task id.
	tasks, err := svc.List(ctx, "pending")
	require.NoError(t, err)
	require.NotEmpty(t, tasks)
	id := tasks[0].ID

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+id, nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	tk, ok := body["task"].(map[string]any)
	require.True(t, ok, "response must include task object")
	require.Equal(t, id, tk["id"])
	_, hasEvents := body["events"]
	require.True(t, hasEvents, "explain response must include events")
	_, hasAttempts := body["attempts"]
	require.True(t, hasAttempts, "explain response must include attempts")
}

func TestGETTaskByIDMissingReturns404(t *testing.T) {
	t.Parallel()

	h, _, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/no-such-id", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGETTaskWhyReturnsReadinessVerdict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc, _ := newServerFixture(t)

	tasks, err := svc.List(ctx, "pending")
	require.NoError(t, err)
	require.NotEmpty(t, tasks)
	id := tasks[0].ID

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+id+"/why", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	_, hasReady := body["ready"]
	require.True(t, hasReady, "why response must include ready field")
	_, hasTask := body["task"]
	require.True(t, hasTask, "why response must include task field")
}

func TestGETReadyReturnsReadyTasks(t *testing.T) {
	t.Parallel()

	h, _, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var tasks []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tasks))
	// Both pending tasks are unblocked so both are ready.
	require.Len(t, tasks, 2)
	for _, tk := range tasks {
		require.Equal(t, "pending", tk["status"])
	}
}

func TestPOSTRequiresCSRFToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc, _ := newServerFixture(t)

	tasks, err := svc.List(ctx, "pending")
	require.NoError(t, err)
	require.NotEmpty(t, tasks)
	id := tasks[0].ID

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/done", nil)
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusPending, got.Status)
}

func TestPOSTDoneTransitionsTaskAndReturnsDone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc, _ := newServerFixture(t)

	tasks, err := svc.List(ctx, "pending")
	require.NoError(t, err)
	require.NotEmpty(t, tasks)
	id := tasks[0].ID

	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/done", nil))
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify the task is now done via the service.
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusDone, got.Status)
}

func TestPOSTUnknownActionReturns400(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc, _ := newServerFixture(t)

	tasks, err := svc.List(ctx, "pending")
	require.NoError(t, err)
	require.NotEmpty(t, tasks)
	id := tasks[0].ID

	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/bogus", nil))
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	errMsg, ok := body["error"].(string)
	require.True(t, ok)
	require.Contains(t, errMsg, "unknown action")
}

func TestPOSTPruneReturnsOK(t *testing.T) {
	t.Parallel()

	h, _, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/prune", nil))
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, true, body["ok"])
}

func TestPOSTPruneRemovesDoneTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc, _ := newServerFixture(t)

	// Confirm done task exists before prune.
	before, err := svc.List(ctx, "done")
	require.NoError(t, err)
	require.Len(t, before, 1)

	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/prune", nil))
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	after, err := svc.List(ctx, "done")
	require.NoError(t, err)
	require.Empty(t, after, "prune must remove done tasks")
}

func TestPOSTFailTransitionsTaskToFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc, _ := newServerFixture(t)

	tasks, err := svc.List(ctx, "pending")
	require.NoError(t, err)
	require.NotEmpty(t, tasks)
	id := tasks[0].ID

	body := `{"error":"test failure reason"}`
	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/fail", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status)
	require.Contains(t, got.Error, "test failure reason")
}
