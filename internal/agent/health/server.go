// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package health serves the /healthz and /readyz probes for the node agent.
package health

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server serves /healthz (liveness) and /readyz (readiness) probes.
// /healthz never depends on StateSource reachability — a Control Plane outage
// must not restart the entire fleet. /readyz reflects simulator readiness.
type Server struct {
	addr  string
	log   *slog.Logger
	ready func() (bool, string)
}

// NewServer returns a Server that will listen on addr.
func NewServer(addr string, log *slog.Logger) *Server {
	return &Server{
		addr:  addr,
		log:   log,
		ready: func() (bool, string) { return true, "ok" },
	}
}

// SetReadiness replaces the readiness probe function. The agent calls this
// after wiring simulators so /readyz reflects per-simulator Ready() state.
func (s *Server) SetReadiness(fn func() (bool, string)) {
	s.ready = fn
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/healthz", writeProbe(func() probe { return probe{true, "ok"} }))
	r.Get("/readyz", writeProbe(func() probe {
		ok, reason := s.ready()
		return probe{ok, reason}
	}))

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	s.log.Info("health server listening", "addr", s.addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("health server: %w", err)
	}
	return nil
}

type probe struct {
	ok     bool
	reason string
}

func writeProbe(check func() probe) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		result := check()
		status := http.StatusOK
		if !result.ok {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = fmt.Fprintln(w, result.reason)
	}
}
