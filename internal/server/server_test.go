package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/server"
	"github.com/dotcommander/afk/internal/store"
	"github.com/dotcommander/afk/internal/task"
	"github.com/stretchr/testify/require"
)

func newServerFixture(t *testing.T) (http.Handler, *app.Service) {
	t.Helper()
	ctx := context.Background()
	st, err := store.NewSQLite(ctx, store.Paths{SQLitePath: filepath.Join(t.TempDir(), "tasks.sqlite")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })

	svc := app.NewService(st, func() time.Time { return time.Now().UTC() })
	_, err = svc.Add(ctx, "todo one")
	require.NoError(t, err)
	_, err = svc.Add(ctx, "todo two")
	require.NoError(t, err)
	doneID, err := svc.Add(ctx, "done task")
	require.NoError(t, err)
	require.NoError(t, svc.SetStatus(ctx, doneID, task.StatusDone, "fixture complete"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(svc, logger, "127.0.0.1:0", false)
	return srv.Handler(), svc
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
	req.Header.Set("X-AFK-CSRF-Token", token)
	return req
}

func TestGETIndexReturns200AndHTML(t *testing.T) {
	t.Parallel()

	h, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), "<html")
	require.Contains(t, rec.Body.String(), "QUEUE HEALTH")
}

func TestGETStatusReturnsCountsAndTasks(t *testing.T) {
	t.Parallel()

	h, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	counts := body["counts"].(map[string]any)
	require.Equal(t, float64(2), counts["todo"])
	require.Equal(t, float64(1), counts["done"])
	_, hasTodo := body["todo"]
	_, hasDoing := body["doing"]
	require.True(t, hasTodo)
	require.True(t, hasDoing)
	health := body["health"].(map[string]any)
	require.Equal(t, float64(86400), health["window_seconds"])
}

func TestGETTasksStatusAndSearch(t *testing.T) {
	t.Parallel()

	h, _ := newServerFixture(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tasks?status=todo&q=one", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var tasks []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tasks))
	require.Len(t, tasks, 1)
	require.Equal(t, "todo", tasks[0]["status"])
	require.Contains(t, tasks[0]["body"], "one")
}

func TestGETTaskReturnsHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc := newServerFixture(t)
	tasks, err := svc.List(ctx, "todo")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tasks/"+tasks[0].ID, nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body["task"])
	_, hasEvents := body["events"]
	_, hasAttempts := body["attempts"]
	require.True(t, hasEvents)
	require.True(t, hasAttempts)
}

func TestPOSTTakeDryRunAndClaim(t *testing.T) {
	t.Parallel()

	h, _ := newServerFixture(t)

	dry := httptest.NewRecorder()
	dryReq := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/take?dry_run=true", nil))
	h.ServeHTTP(dry, dryReq)
	require.Equal(t, http.StatusOK, dry.Code)
	var ready []map[string]any
	require.NoError(t, json.Unmarshal(dry.Body.Bytes(), &ready))
	require.Len(t, ready, 2)

	claim := httptest.NewRecorder()
	claimReq := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/take?worker=w1&lease=30m", nil))
	h.ServeHTTP(claim, claimReq)
	require.Equal(t, http.StatusOK, claim.Code)
	var claimed map[string]any
	require.NoError(t, json.Unmarshal(claim.Body.Bytes(), &claimed))
	require.Equal(t, "doing", claimed["status"])

	for {
		rec := httptest.NewRecorder()
		req := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/take", nil))
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		if strings.TrimSpace(rec.Body.String()) == "null" {
			break
		}
	}
}

func TestPATCHTaskSetsStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc := newServerFixture(t)
	tasks, err := svc.List(ctx, "todo")
	require.NoError(t, err)
	id := tasks[0].ID

	body := `{"status":"failed","error":"test failure reason"}`
	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id, strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status)
	require.Contains(t, got.Error, "test failure reason")
}

func TestPATCHTaskRejectsTerminalStatusWithoutNote(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc := newServerFixture(t)
	tasks, err := svc.List(ctx, "todo")
	require.NoError(t, err)
	id := tasks[0].ID

	body := `{"status":"done"}`
	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id, strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), task.ErrMissingCompletionNote.Error())
	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, task.StatusTodo, got.Status)
}

func TestPATCHTaskUsesNoteAndReasonFallbacks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc := newServerFixture(t)
	tasks, err := svc.List(ctx, "todo")
	require.NoError(t, err)

	for _, body := range []string{
		`{"status":"failed","note":"note reason"}`,
		`{"status":"failed","reason":"fallback reason"}`,
	} {
		id := tasks[0].ID
		rec := httptest.NewRecorder()
		req := withCSRF(t, h, httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id, strings.NewReader(body)))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		got, showErr := svc.Show(ctx, id)
		require.NoError(t, showErr)
		require.Equal(t, task.StatusFailed, got.Status)
		require.NotEmpty(t, got.Error)
		require.NoError(t, svc.SetStatus(ctx, id, task.StatusTodo, "reset"))
	}
}

func TestPOSTCreateInfersRepoDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc := newServerFixture(t)
	repoDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repoDir, ".git"), 0o755))

	body := `{"body":"created from dashboard","cwd":` + strconv.Quote(repoDir) + `}`
	rec := httptest.NewRecorder()
	req := withCSRF(t, h, httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var created map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	id := created["id"]
	require.NotEmpty(t, id)

	got, err := svc.Show(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "created from dashboard", got.Body)
	require.Equal(t, repoDir, got.CWD)
	require.Equal(t, "web", got.Source)
	require.Equal(t, "repo:"+repoDir, got.ResourceKey)
	require.Contains(t, got.Tags, "repo:"+filepath.Base(repoDir))
}

func TestGETPathsAndErrorResponses(t *testing.T) {
	t.Parallel()

	h, svc := newServerFixture(t)
	_, err := svc.AddWithOptions(context.Background(), task.AddOptions{
		Body: "path task",
		CWD:  "/tmp/afk-test-repo",
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/paths", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var paths []string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &paths))
	require.Contains(t, paths, "/tmp/afk-test-repo")

	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/tasks/missing-id", nil))
	require.Equal(t, http.StatusNotFound, missing.Code)
	require.Contains(t, missing.Body.String(), "not found")

	badStatus := httptest.NewRecorder()
	h.ServeHTTP(badStatus, httptest.NewRequest(http.MethodGet, "/api/tasks?status=not-real", nil))
	require.Equal(t, http.StatusBadRequest, badStatus.Code)
	require.Contains(t, badStatus.Body.String(), "invalid task status")
}

func TestMutationHandlersRejectBadInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc := newServerFixture(t)
	tasks, err := svc.List(ctx, "todo")
	require.NoError(t, err)
	id := tasks[0].ID

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
		want   string
	}{
		{name: "patch bad json", method: http.MethodPatch, path: "/api/tasks/" + id, body: "{", want: "decode body"},
		{name: "patch unknown field", method: http.MethodPatch, path: "/api/tasks/" + id, body: `{"status":"done","surprise":true}`, want: `unknown field \"surprise\"`},
		{name: "patch trailing json", method: http.MethodPatch, path: "/api/tasks/" + id, body: `{"status":"done"} {"status":"failed"}`, want: "exactly one JSON value"},
		{name: "patch bad status", method: http.MethodPatch, path: "/api/tasks/" + id, body: `{"status":"nope"}`, want: "invalid task status"},
		{name: "create bad json", method: http.MethodPost, path: "/api/tasks", body: "{", want: "decode body"},
		{name: "create unknown field", method: http.MethodPost, path: "/api/tasks", body: `{"body":"created","extra":true}`, want: `unknown field \"extra\"`},
		{name: "create trailing json", method: http.MethodPost, path: "/api/tasks", body: `{"body":"created"} {"body":"second"}`, want: "exactly one JSON value"},
		{name: "create invalid task", method: http.MethodPost, path: "/api/tasks", body: `{"body":""}`, want: "invalid task"},
		{name: "take bad dry run", method: http.MethodPost, path: "/api/take?dry_run=maybe", body: "", want: "parse dry_run"},
		{name: "take bad lease", method: http.MethodPost, path: "/api/take?lease=soon", body: "", want: "parse lease"},
		{name: "take non-positive lease", method: http.MethodPost, path: "/api/take?lease=0s", body: "", want: "duration must be positive"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := withCSRF(t, h, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), tt.want)
		})
	}
}

func TestMutationHandlersRequireCSRF(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h, svc := newServerFixture(t)
	tasks, err := svc.List(ctx, "todo")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+tasks[0].ID, strings.NewReader(`{"status":"done"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid csrf token")
}

func TestServerRunStartsAndStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	_, svc := newServerFixture(t)
	srv := server.New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), "127.0.0.1:0", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, srv.Run(ctx))
}
