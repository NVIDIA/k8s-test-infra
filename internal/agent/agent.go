// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package agent implements the Mokka Node Agent: a level-triggered reconciler
// and supervisor that compiles desired GPU simulation state and fans it out to
// per-component Simulators in parallel.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/health"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

// Agent is the reconciler and supervisor for all simulators.
type Agent struct {
	simulators []Simulator
	appliers   []Applier
	source     StateSource
	host       *host.Host
	health     *health.Server
	log        *slog.Logger
}

// Config carries Agent constructor arguments.
type Config struct {
	Simulators []Simulator
	Appliers   []Applier
	Source     StateSource
	Host       *host.Host
	Health     *health.Server
	Log        *slog.Logger
}

// New returns an Agent from cfg.
func New(cfg Config) *Agent {
	return &Agent{
		simulators: cfg.Simulators,
		appliers:   cfg.Appliers,
		source:     cfg.Source,
		host:       cfg.Host,
		health:     cfg.Health,
		log:        cfg.Log,
	}
}

// Run starts the agent and blocks until ctx is cancelled or a required component
// fails. On return it executes a best-effort Revoke → Discard teardown.
func (a *Agent) Run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)

	// Supervisor wave: each simulator's Run launches once at startup and
	// survives state changes. Only ctx.Done stops them.
	for _, sim := range a.simulators {
		sim := sim
		g.Go(func() error {
			if err := sim.Run(gctx); err != nil {
				a.log.Error("simulator run exited", "simulator", sim.Name(), "err", err)
			}
			return nil // supervisor errors are non-fatal to the agent
		})
	}

	g.Go(func() error { return a.health.Run(gctx) })
	g.Go(func() error { return a.reconcileLoop(gctx) })

	err := g.Wait()

	// Teardown: Revoke then Discard, best-effort with a fresh context because
	// gctx is already cancelled at this point.
	teardownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
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
				a.log.Error("reconcile failed", "err", err)
			}
		}
	}
}

// reconcile runs Stage on all simulators in parallel, waits for the barrier,
// then runs Apply on all appliers in parallel.
func (a *Agent) reconcile(ctx context.Context, state *State) error {
	stageG, stageCtx := errgroup.WithContext(ctx)
	for _, sim := range a.simulators {
		sim := sim
		stageG.Go(func() error {
			if err := sim.Stage(stageCtx, a.host, state); err != nil {
				return fmt.Errorf("stage %s: %w", sim.Name(), err)
			}
			return nil
		})
	}
	if err := stageG.Wait(); err != nil {
		return err
	}

	applyG, applyCtx := errgroup.WithContext(ctx)
	for _, app := range a.appliers {
		app := app
		applyG.Go(func() error {
			if err := app.Apply(applyCtx, a.host, state); err != nil {
				return fmt.Errorf("apply %T: %w", app, err)
			}
			return nil
		})
	}
	return applyG.Wait()
}

// revoke calls Revoke on all appliers concurrently, best-effort.
func (a *Agent) revoke(ctx context.Context) {
	var g errgroup.Group
	for _, app := range a.appliers {
		app := app
		g.Go(func() error {
			if err := app.Revoke(ctx, a.host); err != nil {
				a.log.Error("revoke failed", "applier", fmt.Sprintf("%T", app), "err", err)
			}
			return nil
		})
	}
	_ = g.Wait()
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
