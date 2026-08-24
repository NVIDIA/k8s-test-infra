// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package health serves the /healthz and /readyz probes for the node agent.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// HealthzResponse is the /healthz response body.
type HealthzResponse struct {
	OK bool `json:"ok"`
}

// ReadyzResponse is the /readyz response body.
// Simulators is populated once simulators are registered (M1.2+).
type ReadyzResponse struct {
	OK         bool                       `json:"ok"`
	Reason     string                     `json:"reason,omitempty"`
	Simulators map[string]SimulatorStatus `json:"simulators"`
}

// SimulatorStatus is one entry in ReadyzResponse.Simulators.
type SimulatorStatus struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// Server serves /healthz (liveness) and /readyz (readiness) probes.
// /healthz reflects simulator Stage health — a Stage failure marks the node
// unhealthy so kubelet restarts the pod. It never depends on StateSource
// reachability; a Control Plane outage must not restart the entire fleet.
// /readyz reflects simulator readiness.
type Server struct {
	addr            string
	log             *slog.Logger
	shutdownTimeout time.Duration
	livenessFunc    func() bool
	readyzFunc      func() map[string]bool // simulator name → simulator ready status
}

// NewServer returns a Server that will listen on addr.
func NewServer(addr string, log *slog.Logger, shutdownTimeout time.Duration) *Server {
	if shutdownTimeout == 0 {
		shutdownTimeout = 5 * time.Second
	}
	return &Server{
		addr:            addr,
		log:             log,
		shutdownTimeout: shutdownTimeout,
		livenessFunc:    func() bool { return true },
		readyzFunc:      func() map[string]bool { return map[string]bool{} },
	}
}

// SetLiveness replaces the liveness probe function. When fn returns false,
// /healthz responds 503 so kubelet restarts the pod.
func (s *Server) SetLiveness(fn func() bool) { s.livenessFunc = fn }

// SetReadiness replaces the readiness probe function. fn returns a name→ready
// map; the server builds the JSON response from it.
func (s *Server) SetReadiness(fn func() map[string]bool) { s.readyzFunc = fn }

// Handler returns the HTTP handler. Useful for testing without binding a port.
func (s *Server) Handler() http.Handler { return s.buildRouter() }

func (s *Server) buildRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		ok := s.livenessFunc()
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, HealthzResponse{OK: ok})
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		statuses := s.readyzFunc()
		resp := ReadyzResponse{OK: true, Simulators: make(map[string]SimulatorStatus, len(statuses))}
		for name, ok := range statuses {
			if !ok {
				resp.OK = false
			}
			resp.Simulators[name] = SimulatorStatus{OK: ok}
		}
		status := http.StatusOK
		if !resp.OK {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, resp)
	})
	return r
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.buildRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	s.log.Info("health server listening", "addr", s.addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("health server: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
