// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package imex implements the IMEX simulator: the capability surface the NVIDIA
// kernel module provides (channel character devices, a /proc/devices overlay
// and the fabric-imex-mgmt capability file), read by the NVIDIA DRA
// compute-domain kubelet plugin, and the IMEX node software (nvidia-imex and
// nvidia-imex-ctl) its ComputeDomain daemon runs.
package imex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/imex"
)

const name = "imex"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator fakes the IMEX kernel surface that is absent on CPU-only nodes.
//
// Main consumers
// - the DRA compute-domain kubelet plugin, which reads the device majors from
// the /proc/devices substitute at startup; its CDI specs carry the device
// numbers, so it never reads the channel nodes
// - the NRI plugin, which injects the staged channel0 into containers that opt
// in with the imex-channels annotation.
type Simulator struct {
	host            *host.Host
	ready           atomic.Bool
	procDevicesPath string // /proc/devices in production; overridden in tests
	nodeSoftware    nodeSoftware
}

// Options supplies the node-local inputs used to stage IMEX node software.
type Options struct {
	// Architecture selects the pinned archive; defaults to the agent's own.
	Architecture string
	// ShimPath is the --nogpu shim published as nvidia-imex.
	ShimPath string
	// ProvidedDir is where the pinned archive is looked up by its file name.
	ProvidedDir string
	// Lock pins the IMEX release; defaults to the embedded imex.lock.json.
	Lock Lock
}

// New returns an imex Simulator.
func New(h *host.Host, options ...Options) *Simulator {
	opt := Options{
		Architecture: runtime.GOARCH,
		ShimPath:     "/usr/local/bin/nvidia-imex-shim",
		ProvidedDir:  DefaultProvidedDir,
		Lock:         DefaultLock(),
	}
	if len(options) > 0 {
		provided := options[0]
		if provided.Architecture != "" {
			opt.Architecture = provided.Architecture
		}
		if provided.ShimPath != "" {
			opt.ShimPath = provided.ShimPath
		}
		if provided.ProvidedDir != "" {
			opt.ProvidedDir = provided.ProvidedDir
		}
		if provided.Lock.Version != "" {
			opt.Lock = provided.Lock
		}
	}
	return &Simulator{
		host:            h,
		procDevicesPath: "/proc/devices",
		nodeSoftware: nodeSoftware{
			root:        h.Root,
			shimPath:    opt.ShimPath,
			providedDir: opt.ProvidedDir,
			arch:        opt.Architecture,
			lock:        opt.Lock,
		},
	}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error. A node
// without IMEX node software is still ready: see nodeSoftware.stage.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the IMEX capability surface and node software under
// host.Root. When state.IMEX.Enabled is false it removes all of it, as Discard
// does, and marks ready: the flag follows the profile and can change at
// runtime.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	if !state.IMEX.Enabled {
		if err := s.remove(); err != nil {
			return fmt.Errorf("remove imex: %w", err)
		}
		s.ready.Store(true)
		zap.L().Info("simulator staged; imex disabled", zap.String("simulator", name))
		return nil
	}

	if err := stageKernelSurface(s.host, state, s.procDevicesPath); err != nil {
		// Fail open: the node becomes one without IMEX, which stays ready and
		// runs no ComputeDomains, rather than an agent that never gets ready.
		zap.L().Warn("cannot simulate the IMEX kernel surface; ComputeDomains fail on this node",
			zap.String("simulator", name), zap.Error(err))
		if err := s.remove(); err != nil {
			return fmt.Errorf("remove imex: %w", err)
		}
		s.ready.Store(true)
		return nil
	}
	s.nodeSoftware.stage()

	s.ready.Store(true)
	zap.L().Info("simulator staged", zap.String("simulator", name))
	return nil
}

// Discard removes every IMEX surface Stage may have written. It does not
// depend on the last Stage having completed: a pass that failed partway can
// leave what an earlier pass staged, and removing what is absent is a no-op.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))
	return s.remove()
}

// remove withdraws the node software, then deletes the capability surface.
func (s *Simulator) remove() error {
	var errs []error
	if err := s.nodeSoftware.remove(); err != nil {
		errs = append(errs, err)
	}
	for _, rel := range []string{
		imex.ChannelsDir,
		filepath.Dir(imex.ProcDevicesPath),
	} {
		p := s.host.RootPath(rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	capFile := s.host.RootPath(imex.FabricIMEXMgmtPath)
	if err := fsutil.Remove(capFile); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
