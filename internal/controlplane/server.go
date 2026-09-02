// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
)

// Server bounds ReadHeaderTimeout so a client that opens a connection and
// dribbles headers cannot hold a handler slot indefinitely.
type Server struct {
	cfg    Config
	logger *slog.Logger
	http   *http.Server
}

// NewServer does not bind a listener; call Run or RunListener for that.
func NewServer(cfg Config, logger *slog.Logger) *Server {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)

	router.Get("/healthz", health.Handler(health.OK))
	router.Get("/readyz", health.Handler(health.OK))

	return &Server{
		cfg:    cfg,
		logger: logger,
		http: &http.Server{
			Addr:              cfg.ListenAddr,
			Handler:           router,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Router exposes the routing surface for tests that drive it through httptest
// without binding a real listener.
func (s *Server) Router() http.Handler {
	return s.http.Handler
}

// Run binds cfg.ListenAddr and delegates to RunListener.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.ListenAddr, err)
	}
	return s.RunListener(ctx, listener)
}

// RunListener serves on the provided listener. Splitting it out from Run lets
// tests bind ":0" and read the resolved port back off the listener before
// exercising the server.
func (s *Server) RunListener(ctx context.Context, listener net.Listener) error {
	serveErr := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", listener.Addr().String())
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.logger.Info("shutdown signal received; draining http server", "timeout", s.cfg.ShutdownTimeout)
		// context.WithoutCancel preserves ctx values (e.g. tracing IDs) while
		// dropping the cancellation that just fired — otherwise Shutdown
		// would return before the drain gets a chance to start.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		return nil
	}
}
