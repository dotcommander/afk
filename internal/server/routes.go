package server

import (
	"net/http"
)

// handler builds the mux.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.serveIndex)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/tasks", s.handleTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleTask)
	mux.HandleFunc("GET /api/tasks/{id}/why", s.handleWhy)
	mux.HandleFunc("GET /api/ready", s.handleReady)
	mux.HandleFunc("POST /api/tasks/{id}/{action}", s.handleAction)
	mux.HandleFunc("POST /api/prune", s.handlePrune)
	mux.HandleFunc("POST /api/tasks", s.handleCreate)
	mux.HandleFunc("GET /api/paths", s.handlePaths)
	return mux
}

func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}
