package server

import (
	"bytes"
	"net/http"
)

// handler builds the mux.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveIndex)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/tasks", s.handleTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.handleSetTask)
	mux.HandleFunc("POST /api/take", s.handleTake)
	mux.HandleFunc("POST /api/tasks", s.handleCreate)
	mux.HandleFunc("GET /api/paths", s.handlePaths)
	return s.csrfGuard(mux)
}

func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodPost || r.Method == http.MethodPatch) && r.Header.Get(csrfHeader) != s.csrfToken {
			writeErr(w, http.StatusForbidden, errInvalidCSRF)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	body := bytes.Replace(indexHTML, []byte("__AFK_CSRF_TOKEN__"), []byte(s.csrfToken), 1)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}
