// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package migcaps implements the MIG capability surface simulator: the
// /dev/nvidia-caps character devices, the capabilities/gpu*/mig procfs tree,
// and the mig-minors table that maps between them.
package migcaps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const name = "migcaps"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator fakes the kernel-level MIG capability surface.
//
// Finding a MIG device through NVML is not enough to use one: a consumer must
// also open the capability device guarding it. nvidia-container-toolkit reads
// mig-minors to map a partition to /dev/nvidia-caps/nvidia-cap<minor>, and
// containerd must find that node on disk before it will admit the pod. Without
// this, a device plugin running migStrategy=single advertises MIG resources
// that no pod can then be given.
type Simulator struct {
	ready atomic.Bool
}

// New returns a migcaps Simulator.
func New() *Simulator { return &Simulator{} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the MIG capability surface under h.Root. It is a no-op
// (but marks ready) when no GPU boots partitioned, because a driver on which
// MIG was never enabled publishes none of these files, and their absence is
// how a consumer detects that.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	if !state.MIG.Partitioned() {
		// Clear any surface left by a previous, partitioned generation:
		// re-staging must not leave cap nodes for partitions that are gone.
		if err := removeSurface(h); err != nil {
			return err
		}
		s.ready.Store(true)
		return nil
	}

	caps := capsFor(state.MIG)

	// Staging is idempotent by construction rather than by check: gpudriver's
	// Discard removes driver/dev wholesale, so a restage of that simulator can
	// take these nodes with it and this one has to be able to put them back.
	if err := stageCapDevs(h, state.MIG.CapsMajor, caps); err != nil {
		return fmt.Errorf("migcaps cap devices: %w", err)
	}
	if err := stageMinors(h, caps); err != nil {
		return fmt.Errorf("migcaps mig-minors: %w", err)
	}
	if err := stageCapabilityTree(h, caps); err != nil {
		return fmt.Errorf("migcaps capabilities tree: %w", err)
	}

	s.ready.Store(true)
	return nil
}

// Discard removes everything Stage created. It is a no-op when Stage never
// completed successfully.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	if !s.ready.Load() {
		return nil
	}
	return removeSurface(h)
}

func removeSurface(h *host.Host) error {
	gpuDirs, err := filepath.Glob(filepath.Join(h.Root, capabilitiesDir, "gpu*"))
	if err != nil {
		return fmt.Errorf("scan %s: %w", capabilitiesDir, err)
	}

	paths := make([]string, 0, 3+len(gpuDirs))
	paths = append(paths,
		filepath.Join(h.Root, capDevDir),
		filepath.Join(h.Root, minorsDir),
		// Only this simulator's subtrees: capabilities/ is shared with the
		// IMEX simulator's fabric-imex-mgmt file.
		filepath.Join(h.Root, capabilitiesDir, "mig"),
	)
	paths = append(paths, gpuDirs...)

	var errs []error
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}
