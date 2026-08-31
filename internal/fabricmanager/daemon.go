// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabricmanager

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// defaultReassertInterval bounds how long an externally deleted marker persists.
const defaultReassertInterval = 2 * time.Second

// Config describes one simulated fabric manager.
type Config struct {
	// StateDir is where the marker is written in this process's mount namespace.
	StateDir string
	// InitDelay reproduces fabric registration latency before readiness appears.
	InitDelay time.Duration
	// ReassertInterval controls how often an externally deleted marker is restored.
	ReassertInterval time.Duration
}

// Daemon publishes fabric readiness for as long as it runs, standing in for
// nv-fabricmanager registering GPUs with the NVSwitches on this node.
type Daemon struct {
	mu           sync.RWMutex
	cfg          Config
	lastStateDir string
	changed      chan struct{}
	log          *slog.Logger
	ready        atomic.Bool
}

// NewDaemon returns a Daemon that publishes readiness under cfg.StateDir.
func NewDaemon(cfg Config) *Daemon {
	if cfg.ReassertInterval <= 0 {
		cfg.ReassertInterval = defaultReassertInterval
	}

	return &Daemon{
		cfg:          cfg,
		lastStateDir: cfg.StateDir,
		changed:      make(chan struct{}),
		log:          slog.Default().With("component", "fabricmanager"),
	}
}

// Reload changes the state directory served by the running daemon. An empty
// directory disables readiness publication until a later reload enables it.
func (d *Daemon) Reload(stateDir string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if stateDir == d.cfg.StateDir {
		return
	}

	d.cfg.StateDir = stateDir

	if stateDir != "" {
		d.lastStateDir = stateDir
	}

	d.ready.Store(false)

	close(d.changed)
	d.changed = make(chan struct{})
}

// Ready reports whether the marker is currently published, or is not needed.
func (d *Daemon) Ready() bool {
	d.mu.RLock()
	disabled := d.cfg.StateDir == ""
	d.mu.RUnlock()

	return disabled || d.ready.Load()
}

// Serve publishes readiness and applies every Reload until ctx is cancelled.
func (d *Daemon) Serve(ctx context.Context) error {
	for {
		cfg, changed := d.snapshot()

		if cfg.StateDir == "" {
			select {
			case <-ctx.Done():
				return nil
			case <-changed:
			}
			continue
		}

		d.serveGen(ctx, cfg, changed)

		if err := d.withdrawReadiness(cfg.StateDir); err != nil {
			d.log.Error("could not withdraw fabric readiness", "err", err)
		}

		if ctx.Err() != nil {
			return nil
		}
	}
}

// Stop withdraws readiness.
func (d *Daemon) Stop() error {
	d.mu.RLock()
	dir := d.lastStateDir
	d.mu.RUnlock()

	d.ready.Store(false)
	return d.withdrawReadiness(dir)
}

func (d *Daemon) snapshot() (Config, <-chan struct{}) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.cfg, d.changed
}

func (d *Daemon) serveGen(ctx context.Context, cfg Config, changed <-chan struct{}) {
	if err := d.withdrawReadiness(cfg.StateDir); err != nil {
		d.log.Warn("could not clear stale readiness marker", "err", err)
	}

	if !d.simRegistration(ctx, changed, cfg.InitDelay) {
		return
	}

	ticker := time.NewTicker(cfg.ReassertInterval)

	defer ticker.Stop()

	for {
		d.assertReadiness(cfg.StateDir)
		select {
		case <-ctx.Done():
			return
		case <-changed:
			return
		case <-ticker.C:
		}
	}
}

// simRegistration simulates the initial delay that real fabric manager has before it becomes ready
func (d *Daemon) simRegistration(ctx context.Context, changed <-chan struct{}, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}

	d.log.Info("simulating fabric registration delay", "delay", delay)
	select {
	case <-ctx.Done():
	case <-changed:
	case <-time.After(delay):
		return true
	}
	return false
}

func (d *Daemon) assertReadiness(stateDir string) {
	if err := WriteReady(stateDir); err != nil {
		d.ready.Store(false)
		d.log.Error("could not write readiness marker", "err", err)
		return
	}

	if d.ready.CompareAndSwap(false, true) {
		d.log.Info("fabric ready", "marker", MarkerPath(stateDir))
	}
}

func (d *Daemon) withdrawReadiness(stateDir string) error {
	d.ready.Store(false)

	if stateDir == "" {
		return nil
	}

	return RemoveReady(stateDir)
}
