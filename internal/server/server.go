// Package server provides the HTTP dashboard server for afk.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dotcommander/afk/internal/app"
)

const shutdownTimeout = 5 * time.Second

// Server serves the afk web dashboard.
type Server struct {
	svc    *app.Service
	logger *slog.Logger
	addr   string
}

// New constructs a Server.
func New(svc *app.Service, logger *slog.Logger, addr string) *Server {
	return &Server{svc: svc, logger: logger, addr: addr}
}

// Handler returns the HTTP mux for use in tests without starting a listener.
func (s *Server) Handler() http.Handler {
	return s.handler()
}

// Run starts listening and blocks until ctx is cancelled or a fatal error occurs.
// On ctx cancellation it initiates a graceful shutdown with a 5-second budget.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-listenErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("server: listen: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server: shutdown: %w", err)
		}
		return nil
	}
}
