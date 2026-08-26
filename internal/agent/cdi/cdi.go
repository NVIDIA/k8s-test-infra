// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package cdi implements the CDI spec applier: it writes two CDI YAML specs
// that containerd uses to inject mock GPU devices into workload containers.
package cdi

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const (
	name = "cdi"
	// Paths are relative to h.Run (/run on the host; /var/run is a symlink to /run).
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
	ready atomic.Bool
}

// New returns a CDI Simulator.
func New() *Simulator { return &Simulator{} }

// Name returns the stable simulator identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether Stage succeeded (always true; Stage is a no-op).
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage is a no-op. Writing a CDI spec before the Stage barrier means the spec's
// hostPaths (chardevs, shims) may not exist yet; containerd fails every container
// creation that references an unresolvable spec. The write is deferred to Apply.
func (s *Simulator) Stage(_ context.Context, _ *host.Host, _ *agent.State) error {
	s.ready.Store(true)
	return nil
}

// Discard is a no-op. Stage wrote nothing, so there is nothing to undo here.
// The specs are published artifacts visible to containerd and are cleaned up in Revoke.
func (s *Simulator) Discard(_ context.Context, _ *host.Host) error { return nil }

// Apply writes /run/cdi/nvidia.yaml and /run/cdi/nvml-mock-nri.yaml.
func (s *Simulator) Apply(_ context.Context, h *host.Host, state *agent.State) error {
	if err := writeSpec(h, filepath.Join(h.Run, nvidiaSpecFile), buildNvidiaSpec(state)); err != nil {
		return fmt.Errorf("nvidia.yaml: %w", err)
	}
	if err := writeSpec(h, filepath.Join(h.Run, nriSpecFile), buildNRISpec(state)); err != nil {
		return fmt.Errorf("nvml-mock-nri.yaml: %w", err)
	}
	return nil
}

// Revoke removes both CDI specs.
func (s *Simulator) Revoke(_ context.Context, h *host.Host) error {
	err1 := h.Remove(filepath.Join(h.Run, nvidiaSpecFile))
	err2 := h.Remove(filepath.Join(h.Run, nriSpecFile))

	if err1 != nil {
		return err1
	}

	return err2
}

func writeSpec(h *host.Host, path string, spec cdiSpec) error {
	data, err := yaml.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return h.WriteFile(path, data, 0o644)
}
