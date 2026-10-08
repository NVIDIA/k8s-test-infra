// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cri implements the container runtime simulator: it owns what the
// node's container runtime needs from Mokka to inject mock GPU devices into
// workload containers. It writes the CDI specs containerd reads and, with a
// Runtime from internal/cri, sets the container runtime up to use them as
// MEP-0006 describes, reverting that when it stops.
package cri

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/NVIDIA/k8s-test-infra/internal/cri"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const (
	name = "cri"
	// Paths are relative to host.Run (/run on the host; /var/run is a symlink to /run).
	nvidiaSpecFile = "cdi/nvidia.yaml"
	nriSpecFile    = "cdi/nvml-mock-nri.yaml"
)

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Applier   = (*Simulator)(nil)
)

// Simulator owns what the container runtime needs to inject mock GPUs: the
// CDI specs nvidia.yaml (nvidia-container-runtime path) and nvml-mock-nri.yaml
// (NRI CDI path), and, with a Runtime, the NVIDIA runtime and its hook and the
// nvidia handler that runs them.
type Simulator struct {
	host  *host.Host
	opts  Options
	ready atomic.Bool
	// standAside is set while another installer owns the runtime setup, so
	// Revoke reverts nothing of theirs.
	standAside atomic.Bool
}

// Options configure the runtime setup. The zero value only publishes the
// specs and leaves the node's container runtime alone.
type Options struct {
	// Runtime registers the handler with the node's container runtime; nil
	// leaves the runtime alone.
	Runtime cri.Runtime
	// ToolkitSource is where the image ships the toolkit binaries.
	ToolkitSource string
}

// New returns a container runtime Simulator.
func New(h *host.Host, opts Options) *Simulator {
	return &Simulator{host: h, opts: opts}
}

// Name returns the stable simulator identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the specs are published and the runtime serves them,
// or stands aside for another installer. The log says why it is not ready, or
// why it stands aside.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage writes no spec. Writing one before the Stage barrier means the spec's
// hostPaths (chardevs, shims) may not exist yet; containerd fails every container
// creation that references an unresolvable spec. The write is deferred to Apply.
//
// Stage does withdraw the NRI spec, for the same reason: gpudriver prunes the
// nodes of GPUs a smaller profile dropped, and the NRI plugin may run between
// Stage and Apply. Without a spec it falls back to raw nodes from the new tree.
// The nvidia spec stays because its consumers have no such fallback.
func (s *Simulator) Stage(_ context.Context, _ *agent.State) error {
	s.ready.Store(false)
	return fsutil.Remove(s.host.RunPath(nriSpecFile))
}

// Discard is a no-op. Stage wrote nothing, so there is nothing to undo here.
// The specs are published artifacts visible to containerd and are cleaned up in Revoke.
func (s *Simulator) Discard(_ context.Context) error { return nil }

// Apply writes /run/cdi/nvidia.yaml and /run/cdi/nvml-mock-nri.yaml and, with
// a Runtime, sets the container runtime up to use them: it installs the
// toolkit binaries before the specs that call the hook, then registers the
// handler. A failed setup is logged and reported through readiness, and does
// not fail the Apply wave: the mock GPUs still reach containers through the
// other paths.
func (s *Simulator) Apply(ctx context.Context, state *agent.State) error {
	zap.L().Info("applying simulator", zap.String("simulator", name))
	s.ready.Store(false)

	if s.opts.Runtime == nil {
		if err := s.publishSpecs(state); err != nil {
			return err
		}

		s.ready.Store(true)
		return nil
	}

	proceed, setupErr := s.prepare()

	if err := s.publishSpecs(state); err != nil {
		return err
	}

	switch {
	case setupErr != nil:
		zap.L().Error("container runtime setup failed", zap.String("simulator", name), zap.Error(setupErr))
	case proceed:
		s.ready.Store(s.setUp(ctx))
	default:
		// The simulator stands aside, and prepare logged why.
		s.ready.Store(true)
	}

	return nil
}

// prepare installs the toolkit, unless the node has no container runtime to set
// up or another installer owns the toolkit. It reports whether to go on and set
// the runtime up. With neither that nor an error, the simulator stands aside,
// and prepare has logged why.
func (s *Simulator) prepare() (bool, error) {
	if err := s.opts.Runtime.Installed(); err != nil {
		s.standAside.Store(true)
		zap.L().Warn("the container runtime is not installed on this node; leaving the node alone, so pods here get no mock driver from it. "+
			"Set nodeAgent.containerRuntime.enabled=false for nodes that run another runtime",
			zap.String("simulator", name), zap.Error(err))

		return false, nil
	}

	foreign, err := cri.ForeignToolkit(s.host)
	if err != nil {
		return false, err
	}

	s.standAside.Store(len(foreign) > 0)
	if len(foreign) > 0 {
		zap.L().Warn("another installer owns the NVIDIA container toolkit on this node; leaving the container runtime alone",
			zap.String("simulator", name), zap.Strings("paths", foreign))

		return false, nil
	}

	if err := cri.InstallToolkit(s.host, s.opts.ToolkitSource); err != nil {
		return false, fmt.Errorf("install the toolkit: %w", err)
	}

	return true, nil
}

// setUp registers the handler with the runtime and reports whether the
// simulator is ready.
func (s *Simulator) setUp(ctx context.Context) bool {
	err := s.opts.Runtime.Setup(ctx)

	switch {
	case err == nil:
		zap.L().Info(
			"container runtime set up",
			zap.String("simulator", name),
			zap.String("handler", cri.HandlerName),
		)

		return true
	case errors.Is(err, cri.ErrRestartPending):
		zap.L().Warn(
			"container runtime configuration written; it takes effect when the runtime restarts",
			zap.String("simulator", name),
			zap.Error(err),
		)

		return true
	default:
		zap.L().Error(
			"container runtime setup failed",
			zap.String("simulator", name),
			zap.Error(err),
		)

		return false
	}
}

// Revoke removes both CDI specs and, with a Runtime, reverts the runtime
// setup as the toolkit's installer does when it stops, unless another
// installer owns the toolkit on this node.
func (s *Simulator) Revoke(ctx context.Context) error {
	zap.L().Info("revoking simulator", zap.String("simulator", name))
	s.ready.Store(false)

	err := errors.Join(
		fsutil.Remove(s.host.RunPath(nvidiaSpecFile)),
		fsutil.Remove(s.host.RunPath(nriSpecFile)),
	)

	if s.opts.Runtime != nil {
		err = errors.Join(err, s.revert(ctx))
	}

	return err
}

func (s *Simulator) revert(ctx context.Context) error {
	if s.standAside.Load() {
		zap.L().Debug(
			"leaving the container runtime as found; this simulator did not set it up",
			zap.String("simulator", name),
		)

		return nil
	}

	if err := s.opts.Runtime.Cleanup(ctx); err != nil {
		return fmt.Errorf("revert the container runtime setup: %w", err)
	}

	return nil
}

// publishSpecs writes both CDI specs.
func (s *Simulator) publishSpecs(state *agent.State) error {
	if err := writeSpec(s.host.RunPath(nvidiaSpecFile), buildNvidiaSpec(state)); err != nil {
		return fmt.Errorf("nvidia.yaml: %w", err)
	}

	if err := writeSpec(s.host.RunPath(nriSpecFile), buildNRISpec(state)); err != nil {
		return fmt.Errorf("nvml-mock-nri.yaml: %w", err)
	}

	return nil
}
