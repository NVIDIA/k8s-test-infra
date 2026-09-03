// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cdi implements the CDI spec applier: it writes two CDI YAML specs
// that containerd uses to inject mock GPU devices into workload containers.
package cdi

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const (
	name = "cdi"
	// Paths are relative to host.Run (/run on the host; /var/run is a symlink to /run).
	nvidiaSpecFile = "cdi/nvidia.yaml"
	nriSpecFile    = "cdi/nvml-mock-nri.yaml"
)

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Applier   = (*Simulator)(nil)
)

// Simulator owns the CDI specs containerd needs to inject mock GPUs:
// nvidia.yaml (nvidia-container-runtime path) and nvml-mock-nri.yaml (NRI CDI path).
type Simulator struct {
	host  *host.Host
	ready atomic.Bool
}

// New returns a CDI Simulator.
func New(h *host.Host) *Simulator { return &Simulator{host: h} }

// Name returns the stable simulator identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether Stage succeeded (always true; Stage is a no-op).
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage is a no-op. Writing a CDI spec before the Stage barrier means the spec's
// hostPaths (chardevs, shims) may not exist yet; containerd fails every container
// creation that references an unresolvable spec. The write is deferred to Apply.
func (s *Simulator) Stage(_ context.Context, _ *agent.State) error {
	s.ready.Store(true)
	return nil
}

// Discard is a no-op. Stage wrote nothing, so there is nothing to undo here.
// The specs are published artifacts visible to containerd and are cleaned up in Revoke.
func (s *Simulator) Discard(_ context.Context) error { return nil }

// Apply writes /run/cdi/nvidia.yaml and /run/cdi/nvml-mock-nri.yaml.
func (s *Simulator) Apply(_ context.Context, state *agent.State) error {
	zap.L().Debug("applying simulator", zap.String("simulator", name))
	if err := writeSpec(s.host.RunPath(nvidiaSpecFile), buildNvidiaSpec(state)); err != nil {
		return fmt.Errorf("nvidia.yaml: %w", err)
	}

	if err := writeSpec(s.host.RunPath(nriSpecFile), buildNRISpec(state)); err != nil {
		return fmt.Errorf("nvml-mock-nri.yaml: %w", err)
	}

	return nil
}

// Revoke removes both CDI specs.
func (s *Simulator) Revoke(_ context.Context) error {
	zap.L().Debug("revoking simulator", zap.String("simulator", name))

	err1 := fsutil.Remove(s.host.RunPath(nvidiaSpecFile))
	err2 := fsutil.Remove(s.host.RunPath(nriSpecFile))

	if err1 != nil {
		return err1
	}

	return err2
}

func writeSpec(path string, spec cdiSpec) error {
	data, err := yaml.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return fsutil.Write(path, data, 0o644)
}
