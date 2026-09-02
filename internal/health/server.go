// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package health

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
)

// DefaultShutdownTimeout bounds the probe listener's drain.
const DefaultShutdownTimeout = 5 * time.Second

// Server is the probe listener for a binary that serves no other HTTP traffic.
// Both probes pass until SetLiveness and SetReadiness supply the real checks.
type Server struct {
	addr            string
	shutdownTimeout time.Duration
	liveness        Checker
	readiness       Checker
}

// NewServer returns a Server that will listen on addr. An empty addr disables
// the probes.
func NewServer(addr string, shutdownTimeout time.Duration) *Server {
	if shutdownTimeout == 0 {
		shutdownTimeout = DefaultShutdownTimeout
	}
	return &Server{
		addr:            addr,
		shutdownTimeout: shutdownTimeout,
		liveness:        OK,
		readiness:       OK,
	}
}

// SetLiveness replaces the /healthz check. A failing liveness probe asks the
// kubelet to restart the pod, so it belongs to states the process cannot leave
// on its own.
func (s *Server) SetLiveness(check Checker) { s.liveness = check }

// SetReadiness replaces the /readyz check.
func (s *Server) SetReadiness(check Checker) { s.readiness = check }

// Handler returns the routes, letting tests drive them without binding a port.
// The checks are read per request, so SetLiveness and SetReadiness still take
// effect after this is called.
func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Get("/healthz", Handler(func() Probe { return s.liveness() }))
	router.Get("/readyz", Handler(func() Probe { return s.readiness() }))
	return router
}

// Run binds addr and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if s.addr == "" {
		slog.Warn("health probes disabled; no probe address configured")
		return nil
	}

	// Bind before serving so a port clash fails startup loudly. A process left
	// running with no probe surface reads as healthy forever.
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.addr, err)
	}

	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		// WithoutCancel keeps ctx values while dropping the cancellation that
		// just fired, so the drain gets its full timeout.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("serving health probes", "addr", listener.Addr().String())
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health server: %w", err)
	}
	return nil
}
