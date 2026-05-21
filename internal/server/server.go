// Package server provides the HTTP dashboard server for afk.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/dotcommander/afk/internal/app"
)

const (
	csrfHeader      = "X-AFK-CSRF-Token"
	shutdownTimeout = 5 * time.Second
)

// Server serves the afk web dashboard.
type Server struct {
	svc         *app.Service
	logger      *slog.Logger
	addr        string
	openBrowser bool
	csrfToken   string
}

// New constructs a Server.
func New(svc *app.Service, logger *slog.Logger, addr string, openBrowser bool) *Server {
	return &Server{svc: svc, logger: logger, addr: addr, openBrowser: openBrowser, csrfToken: newCSRFToken()}
}

func newCSRFToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Errorf("server: csrf token: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// Handler returns the HTTP mux for use in tests without starting a listener.
func (s *Server) Handler() http.Handler {
	return s.handler()
}

// Run starts listening and blocks until ctx is cancelled or a fatal error occurs.
// On ctx cancellation it initiates a graceful shutdown with a 5-second budget.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", s.addr, err)
	}

	if s.openBrowser {
		url := "http://" + ln.Addr().String() + "/"
		if err := launchBrowser(url); err != nil {
			s.logger.Warn("could not open browser", "url", url, "err", err)
		}
	}

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- srv.Serve(ln)
	}()

	select {
	case err := <-listenErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("server: serve: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server: shutdown: %w", err)
		}
		return nil
	}
}
