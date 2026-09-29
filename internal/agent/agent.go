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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/NVIDIA/k8s-test-infra/internal/health"
	"github.com/NVIDIA/k8s-test-infra/internal/staginggate"
)

// Agent is the reconciler and supervisor for all simulators.
type Agent struct {
	simulators      []Simulator
	source          StateSource
	log             *zap.Logger
	shutdownTimeout time.Duration
	stagingLockPath string
	live            atomic.Pointer[health.Probe] // last Stage wave outcome, served on /healthz
	staged          atomic.Bool                  // latest Stage wave completed; false while restaging and during teardown

	// Daemons launch into this group from reconcile but bind to supervisorCtx,
	// so they outlive the reconcile that started them.
	supervisor    *errgroup.Group
	supervisorCtx context.Context //nolint:containedctx // agent lifetime, outlives each reconcile
	// started needs no mutex: reconcile only runs on reconcileLoop's goroutine.
	started map[string]bool
}

// Config carries Agent constructor arguments.
type Config struct {
	Simulators      []Simulator
	Source          StateSource
	Log             *zap.Logger
	ShutdownTimeout time.Duration
	StagingLockPath string
}

// New returns an Agent from cfg.
func New(cfg Config) *Agent {
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	a := &Agent{
		simulators:      cfg.Simulators,
		source:          cfg.Source,
		log:             cfg.Log,
		shutdownTimeout: cfg.ShutdownTimeout,
		stagingLockPath: cfg.StagingLockPath,
		started:         make(map[string]bool),
	}
	a.setLive(health.OK())
	return a
}

func (a *Agent) setLive(probe health.Probe) { a.live.Store(&probe) }

// Liveness passes while the last Stage wave completed without error, naming the
// simulators that failed when it did. It recovers once Stage succeeds again.
func (a *Agent) Liveness() health.Probe { return *a.live.Load() }

// Staged passes once the latest Stage wave has written the tree NRI injects.
// It closes during restaging and teardown, and stays closed after a failed
// Stage. It ignores Apply and daemon readiness, including an intentional
// fabricmanager initialization delay.
func (a *Agent) Staged() health.Probe {
	if a.staged.Load() {
		return health.OK()
	}
	return health.Unhealthy("driver tree not staged")
}

// Readiness aggregates every simulator's readiness and attributes the result
// per simulator, so a red /readyz names which one is not serving.
func (a *Agent) Readiness() health.Probe {
	probe := health.Probe{OK: true, Components: make(map[string]health.Probe, len(a.simulators))}
	for _, sim := range a.simulators {
		ready := sim.Ready()
		if !ready {
			probe.OK = false
		}
		probe.Components[sim.Name()] = health.Probe{OK: ready}
	}
	return probe
}

// Run starts the agent and blocks until ctx is cancelled or a required component
// fails. On return it executes a best-effort Revoke → Discard teardown.
func (a *Agent) Run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)

	// Daemons launch from reconcile, not here — they need their Stage artifacts
	// on disk first. Publishing the group up front does not race errgroup's
	// Go-after-Wait rule: reconcile runs inside reconcileLoop, so Wait cannot
	// advance while a Go call is still possible.
	a.supervisor, a.supervisorCtx = g, gctx

	a.log.Info("agent started; watching state source", zap.Int("simulators", len(a.simulators)))

	g.Go(func() error { return a.reconcileLoop(gctx) })

	err := g.Wait()

	a.log.Info("agent stopping; tearing down simulators")
	a.staged.Store(false)

	// Teardown: Revoke then Discard, best-effort with a fresh context because
	// gctx is already cancelled at this point.
	teardownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.shutdownTimeout)
	defer cancel()
	teardown := func() error {
		a.revoke(teardownCtx)
		a.discard(teardownCtx)
		return nil
	}
	// In-flight NRI decisions get at most half the budget, so teardown keeps
	// the other half even when a wedged plugin never releases the lock.
	lockCtx, cancelLock := context.WithTimeout(teardownCtx, a.shutdownTimeout/2)
	defer cancelLock()
	teardownErr := a.withStagingLock(lockCtx, teardown)
	if errors.Is(teardownErr, errStagingLockUnavailable) {
		// The pod is terminating and the grace period is finite. /stagedz
		// already reports unstaged, so NRI starts no new decisions; only one
		// already in flight can race this teardown.
		a.log.Warn("staging lock unavailable; tearing down without it", zap.Error(teardownErr))
		teardownErr = teardown()
	}

	return errors.Join(err, teardownErr)
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
				a.log.Warn("state source error; keeping cached state", zap.Error(u.Err))
				continue
			}
			if err := a.reconcile(ctx, u.State); err != nil {
				a.log.Error("reconcile failed", zap.Int64("generation", u.State.Generation), zap.Error(err))
			} else {
				a.log.Debug("reconcile succeeded", zap.Int64("generation", u.State.Generation))
			}
		}
	}
}

// reconcile runs Stage on all simulators in parallel, waits for the barrier,
// starts (or reloads) the daemons, then runs Apply on all appliers in parallel.
func (a *Agent) reconcile(ctx context.Context, state *State) error {
	return a.withStagingLock(ctx, func() error { return a.reconcileLocked(ctx, state) })
}

// errStagingLockUnavailable marks a failure to take the staging lock, as
// opposed to a failure of the work the lock guards.
var errStagingLockUnavailable = errors.New("staging lock unavailable")

// withStagingLock runs fn while holding the exclusive staging lock, so no NRI
// adjustment is decided against a tree that is being changed.
func (a *Agent) withStagingLock(ctx context.Context, fn func() error) (err error) {
	if a.stagingLockPath == "" {
		return fn()
	}
	lock, err := staginggate.Exclusive(ctx, a.stagingLockPath)
	if err != nil {
		return fmt.Errorf("%w: %w", errStagingLockUnavailable, err)
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	return fn()
}

func (a *Agent) reconcileLocked(ctx context.Context, state *State) error {
	a.staged.Store(false)
	// Stage wave: all simulators run concurrently and are fully isolated from
	// each other — a failure never cancels sibling goroutines. All errors are
	// collected; if any Stage failed the Apply wave is skipped entirely, because
	// appliers depend on Stage artifacts being present (e.g. CDI spec → chardevs).
	var (
		wg          sync.WaitGroup
		stageMu     sync.Mutex
		stageErrs   []error
		stageFailed []string
	)
	for _, sim := range a.simulators {
		sim := sim
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sim.Stage(ctx, state); err != nil {
				a.log.Error("stage failed", zap.String("simulator", sim.Name()), zap.Error(err))
				stageMu.Lock()
				stageErrs = append(stageErrs, fmt.Errorf("stage %s: %w", sim.Name(), err))
				stageFailed = append(stageFailed, sim.Name())
				stageMu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(stageErrs) > 0 {
		slices.Sort(stageFailed)
		a.setLive(health.Unhealthy("stage failed: %s", strings.Join(stageFailed, ", ")))
		return errors.Join(stageErrs...)
	}

	a.setLive(health.OK())
	// NRI injects the tree Stage has just written. Apply publishes nothing NRI
	// needs (the CDI spec it can reference is checked before use), and a failed
	// Apply is not retried until the state changes, so gating on it would stop
	// injection on this node until then.
	a.staged.Store(true)

	// Supervisor wave sits on the barrier: a daemon starts against surfaces Stage
	// has written, and before Apply publishes them off-node.
	a.supervise(ctx, state)

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
			if err := app.Apply(applyCtx, state); err != nil {
				return fmt.Errorf("apply %s: %w", applierName(app), err)
			}
			return nil
		})
	}
	return applyG.Wait()
}

// supervise launches each Daemon's Run once and delivers later States via
// Reload. Once, because a daemon owns a socket or port that a second instance
// would fight over rather than converge with.
func (a *Agent) supervise(ctx context.Context, state *State) {
	for _, sim := range a.simulators {
		d, ok := sim.(Daemon)
		if !ok {
			continue
		}

		if a.started[sim.Name()] {
			// Non-fatal: the daemon keeps serving the previous state, which
			// beats tearing it down.
			if err := d.Reload(ctx, state); err != nil {
				a.log.Error("daemon reload failed", zap.String("simulator", sim.Name()), zap.Error(err))
			}
			continue
		}

		a.started[sim.Name()] = true
		a.log.Info("starting simulator daemon", zap.String("simulator", sim.Name()))
		a.supervisor.Go(func() error {
			if err := d.Run(a.supervisorCtx); err != nil {
				a.log.Error("simulator daemon exited", zap.String("simulator", sim.Name()), zap.Error(err))
			}
			return nil // daemon errors are non-fatal to the agent
		})
	}
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
			if err := app.Revoke(ctx); err != nil {
				a.log.Error("revoke failed", zap.String("applier", applierName(app)), zap.Error(err))
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
			if err := sim.Discard(ctx); err != nil {
				a.log.Error("discard failed", zap.String("simulator", sim.Name()), zap.Error(err))
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
