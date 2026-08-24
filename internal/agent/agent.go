// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package agent implements the Mokka Node Agent: a level-triggered reconciler
// and supervisor that compiles desired GPU simulation state and fans it out to
// per-component Simulators in parallel.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// Agent is the reconciler and supervisor for all simulators.
type Agent struct {
	simulators      []Simulator
	source          StateSource
	host            *host.Host
	log             *slog.Logger
	shutdownTimeout time.Duration
	live            atomic.Bool // true = Stage healthy; false = /healthz returns 503
}

// Config carries Agent constructor arguments.
type Config struct {
	Simulators      []Simulator
	Source          StateSource
	Host            *host.Host
	Log             *slog.Logger
	ShutdownTimeout time.Duration
}

// New returns an Agent from cfg.
func New(cfg Config) *Agent {
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	a := &Agent{
		simulators:      cfg.Simulators,
		source:          cfg.Source,
		host:            cfg.Host,
		log:             cfg.Log,
		shutdownTimeout: cfg.ShutdownTimeout,
	}
	a.live.Store(true)
	return a
}

// Live returns true when the last Stage wave completed without error.
// Returns false after a Stage failure; recovers once Stage succeeds again.
func (a *Agent) Live() bool { return a.live.Load() }

// Run starts the agent and blocks until ctx is cancelled or a required component
// fails. On return it executes a best-effort Revoke → Discard teardown.
func (a *Agent) Run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)

	// Supervisor wave: launch background daemons once at startup.
	// Only simulators that implement Daemon get a goroutine here.
	for _, sim := range a.simulators {
		sim := sim
		d, ok := sim.(Daemon)
		if !ok {
			continue
		}
		g.Go(func() error {
			if err := d.Run(gctx); err != nil {
				a.log.Error("simulator daemon exited", "simulator", sim.Name(), "err", err)
			}
			return nil // daemon errors are non-fatal to the agent
		})
	}

	g.Go(func() error { return a.reconcileLoop(gctx) })

	err := g.Wait()

	// Teardown: Revoke then Discard, best-effort with a fresh context because
	// gctx is already cancelled at this point.
	teardownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.shutdownTimeout)
	defer cancel()
	a.revoke(teardownCtx)
	a.discard(teardownCtx)

	return err
}

func (a *Agent) reconcileLoop(ctx context.Context) error {
	updates := a.source.Watch(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-updates:
			if !ok {
				return nil // source closed
			}
			if u.Err != nil {
				a.log.Warn("state source error; keeping cached state", "err", u.Err)
				continue
			}
			if err := a.reconcile(ctx, u.State); err != nil {
				a.log.Error("reconcile failed", "generation", u.State.Generation, "err", err)
			}
		}
	}
}

// reconcile runs Stage on all simulators in parallel, waits for the barrier,
// then runs Apply on all appliers in parallel.
func (a *Agent) reconcile(ctx context.Context, state *State) error {
	// Stage wave: all simulators run concurrently and are fully isolated from
	// each other — a failure never cancels sibling goroutines. All errors are
	// collected; if any Stage failed the Apply wave is skipped entirely, because
	// appliers depend on Stage artifacts being present (e.g. CDI spec → chardevs).
	var (
		wg        sync.WaitGroup
		stageMu   sync.Mutex
		stageErrs []error
	)
	for _, sim := range a.simulators {
		sim := sim
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sim.Stage(ctx, a.host, state); err != nil {
				a.log.Error("stage failed", "simulator", sim.Name(), "err", err)
				stageMu.Lock()
				stageErrs = append(stageErrs, fmt.Errorf("stage %s: %w", sim.Name(), err))
				stageMu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(stageErrs) > 0 {
		a.live.Store(false)
		return errors.Join(stageErrs...)
	}

	a.live.Store(true)

	// Apply wave: fail-fast — appliers share cross-component dependencies
	// (CDI spec references chardevs that gpudriver must have staged first).
	// Only simulators that also implement Applier participate.
	applyG, applyCtx := errgroup.WithContext(ctx)
	for _, sim := range a.simulators {
		app, ok := sim.(Applier)
		if !ok {
			continue
		}
		applyG.Go(func() error {
			if err := app.Apply(applyCtx, a.host, state); err != nil {
				return fmt.Errorf("apply %s: %w", applierName(app), err)
			}
			return nil
		})
	}
	return applyG.Wait()
}

// revoke calls Revoke on all Applier simulators concurrently, best-effort.
func (a *Agent) revoke(ctx context.Context) {
	var g errgroup.Group
	for _, sim := range a.simulators {
		app, ok := sim.(Applier)
		if !ok {
			continue
		}

		g.Go(func() error {
			if err := app.Revoke(ctx, a.host); err != nil {
				a.log.Error("revoke failed", "applier", applierName(app), "err", err)
			}
			return nil
		})
	}
	_ = g.Wait()
}

// applierName returns a human-readable name for an Applier.
// Appliers that also implement Simulator expose their Name(); others fall back
// to the Go type name which is always unique and deterministic.
func applierName(a Applier) string {
	if n, ok := a.(interface{ Name() string }); ok {
		return n.Name()
	}
	return fmt.Sprintf("%T", a)
}

// discard calls Discard on all simulators concurrently, best-effort.
func (a *Agent) discard(ctx context.Context) {
	var g errgroup.Group
	for _, sim := range a.simulators {
		sim := sim
		g.Go(func() error {
			if err := sim.Discard(ctx, a.host); err != nil {
				a.log.Error("discard failed", "simulator", sim.Name(), "err", err)
			}
			return nil
		})
	}
	_ = g.Wait()
}
