// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabricmanager

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// defaultReassertInterval is how often the marker is rewritten. The mock NVML
// engine caches its readiness check for a second, so this bounds how long a
// GPU can observe a marker that something else removed.
const defaultReassertInterval = 2 * time.Second

// Config describes one simulated fabric manager.
type Config struct {
	// StateDir is where the marker is written, in this process's mount
	// namespace. It need not equal the path readers use: the agent writes
	// through its host mount, workloads read through a CDI bind.
	StateDir string
	// InitDelay withholds readiness after Serve starts, reproducing the window
	// during which real GPUs report IN_PROGRESS while registering.
	InitDelay time.Duration
	// ReassertInterval overrides how often the marker is rewritten.
	ReassertInterval time.Duration
}

// Daemon publishes fabric readiness for as long as it runs, standing in for
// nv-fabricmanager registering GPUs with the NVSwitches on this node.
type Daemon struct {
	cfg   Config
	log   *slog.Logger
	ready atomic.Bool
}

// NewDaemon returns a Daemon that publishes readiness under cfg.StateDir.
func NewDaemon(cfg Config) *Daemon {
	if cfg.ReassertInterval <= 0 {
		cfg.ReassertInterval = defaultReassertInterval
	}

	return &Daemon{
		cfg: cfg,
		log: slog.Default().With("component", "fabricmanager"),
	}
}

// Ready reports whether the marker is currently published.
func (d *Daemon) Ready() bool { return d.ready.Load() }

// Serve publishes readiness and holds it until ctx is cancelled.
func (d *Daemon) Serve(ctx context.Context) error {
	// The state dir outlives the pod, so a marker left by a previous one would
	// report COMPLETED before this daemon had registered anything.
	if err := RemoveReady(d.cfg.StateDir); err != nil {
		d.log.Warn("could not clear stale readiness marker", "err", err)
	}

	if !d.awaitRegistration(ctx) {
		return nil
	}

	t := time.NewTicker(d.cfg.ReassertInterval)
	defer t.Stop()
	for {
		d.assert()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Stop withdraws readiness.
func (d *Daemon) Stop() error {
	d.ready.Store(false)
	return RemoveReady(d.cfg.StateDir)
}

// awaitRegistration simulates the latency of registering with the NVSwitches.
// Reports false when ctx was cancelled first.
func (d *Daemon) awaitRegistration(ctx context.Context) bool {
	if d.cfg.InitDelay <= 0 {
		return true
	}

	d.log.Info("simulating fabric registration delay", "delay", d.cfg.InitDelay)

	select {
	case <-ctx.Done():
		return false
	case <-time.After(d.cfg.InitDelay):
		return true
	}
}

// assert rewrites the marker, logging only transitions so a 2s loop does not
// fill the log with confirmations that nothing changed.
func (d *Daemon) assert() {
	if err := WriteReady(d.cfg.StateDir); err != nil {
		d.ready.Store(false)
		d.log.Error("could not write readiness marker", "err", err)
		return
	}

	if d.ready.CompareAndSwap(false, true) {
		d.log.Info("fabric ready", "marker", MarkerPath(d.cfg.StateDir))
	}
}
