// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package imex implements the IMEX capability surface simulator:
// channel character devices, a /proc/devices overlay, and the
// fabric-imex-mgmt capability file consumed by the NVIDIA DRA
// compute-domain kubelet plugin.
package imex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const name = "imex"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator fakes the IMEX kernel surface that is absent on CPU-only nodes
//
// Main consumers
// - the DRA compute-domain kubelet plugin (discover device majors at startup)
// - containerd (must find the channel chardevs on disk before it admits a pod
// carrying a compute-domain CDI spec).
type Simulator struct {
	host            *host.Host
	ready           atomic.Bool
	procDevicesPath string // /proc/devices in production; overridden in tests
	userspace       *userspaceInstaller
}

// Options supplies the node-local inputs used to stage IMEX userspace.
type Options struct {
	Architecture string
	ShimPath     string
	Lock         Lock
	HTTPClient   *http.Client
}

// New returns an imex Simulator.
func New(h *host.Host, options ...Options) *Simulator {
	opt := Options{
		Architecture: runtime.GOARCH,
		ShimPath:     "/usr/local/bin/nvidia-imex-shim",
		Lock:         mustDefaultLock(),
		HTTPClient:   &http.Client{Timeout: 5 * time.Minute},
	}
	if len(options) > 0 {
		provided := options[0]
		if provided.Architecture != "" {
			opt.Architecture = provided.Architecture
		}
		if provided.ShimPath != "" {
			opt.ShimPath = provided.ShimPath
		}
		if provided.Lock.Version != "" {
			opt.Lock = provided.Lock
		}
		if provided.HTTPClient != nil {
			opt.HTTPClient = provided.HTTPClient
		}
	}
	return &Simulator{
		host:            h,
		procDevicesPath: "/proc/devices",
		userspace: &userspaceInstaller{
			root:       h.Root,
			shimPath:   opt.ShimPath,
			arch:       opt.Architecture,
			lock:       opt.Lock,
			httpClient: opt.HTTPClient,
		},
	}
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage materializes the IMEX capability surface under host.Root.
// It is a no-op (but marks ready) when state.IMEX.Enabled is false.
func (s *Simulator) Stage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))
	if !state.IMEX.NodeSoftwareEnabled {
		if err := s.reconcileUserspace(ctx, false); err != nil {
			return err
		}
	}

	if !state.IMEX.Enabled && !state.IMEX.NodeSoftwareEnabled {
		s.ready.Store(true)
		zap.L().Info("simulator staged; imex disabled", zap.String("simulator", name))
		return nil
	}

	if state.IMEX.Enabled {
		if err := s.stageKernelSurface(state); err != nil {
			return err
		}
	}
	if state.IMEX.NodeSoftwareEnabled {
		if err := s.reconcileUserspace(ctx, true); err != nil {
			return err
		}
	}

	s.ready.Store(true)
	zap.L().Info("simulator staged", zap.String("simulator", name))
	return nil
}

func (s *Simulator) reconcileUserspace(ctx context.Context, enabled bool) error {
	if enabled {
		if err := s.userspace.stage(ctx); err != nil {
			return fmt.Errorf("imex userspace: %w", err)
		}
		return nil
	}
	if err := s.userspace.discard(); err != nil {
		return fmt.Errorf("remove disabled imex userspace: %w", err)
	}
	return nil
}

func (s *Simulator) stageKernelSurface(state *agent.State) error {
	if err := stageChannelDevs(s.host, state); err != nil {
		return fmt.Errorf("imex channel devs: %w", err)
	}
	if err := stageProcDevices(s.host, state, s.procDevicesPath); err != nil {
		return fmt.Errorf("imex proc-devices: %w", err)
	}
	if err := stageFabricImexMgmt(s.host); err != nil {
		return fmt.Errorf("imex fabric-imex-mgmt: %w", err)
	}
	return nil
}

// Discard removes all IMEX surfaces staged by Stage.
// It is a no-op when Stage never completed successfully.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))
	s.ready.Store(false)

	var errs []error
	for _, rel := range []string{"driver/dev/nvidia-caps-imex-channels"} {
		p := s.host.RootPath(rel)
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	for _, rel := range []string{
		"driver/proc/devices",
		"driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt",
	} {
		if err := fsutil.Remove(s.host.RootPath(rel)); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.userspace.discard(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
