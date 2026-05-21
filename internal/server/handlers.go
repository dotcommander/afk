package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dotcommander/afk/internal/app"
)

var errInvalidCSRF = errors.New("invalid csrf token")

// writeJSON encodes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr emits {"error":"<msg>"} with the given status code.
func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// resolveID reads {id} from the path and writes a 400 if absent.
// Returns ("", false) when the caller should abort.
func resolveID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing task id"))
		return "", false
	}
	return id, true
}

// writeResult encodes v on success, maps ErrNotFound → 404, other errors → 500.
func writeResult(w http.ResponseWriter, v any, err error) {
	if err != nil {
		if errors.Is(err, app.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleStatus serves GET /api/status → StatusSnapshot.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap, err := s.svc.Status(r.Context())
	writeResult(w, snap, err)
}

// handleTasks serves GET /api/tasks[?status=<filter>] → []task.Task.
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.List(r.Context(), r.URL.Query().Get("status"))
	writeResult(w, tasks, err)
}

// handleTask serves GET /api/tasks/{id} → ExplainData.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	id, ok := resolveID(w, r)
	if !ok {
		return
	}
	data, err := s.svc.Explain(r.Context(), id)
	writeResult(w, data, err)
}

// handleWhy serves GET /api/tasks/{id}/why → ReadinessData.
func (s *Server) handleWhy(w http.ResponseWriter, r *http.Request) {
	id, ok := resolveID(w, r)
	if !ok {
		return
	}
	data, err := s.svc.Why(r.Context(), id)
	writeResult(w, data, err)
}

// handleReady serves GET /api/ready → []task.Task.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.Ready(r.Context())
	writeResult(w, tasks, err)
}

// handlePaths serves GET /api/paths — recently used task working directories.
func (s *Server) handlePaths(w http.ResponseWriter, r *http.Request) {
	paths, err := s.svc.RecentPaths(r.Context())
	writeResult(w, paths, err)
}
