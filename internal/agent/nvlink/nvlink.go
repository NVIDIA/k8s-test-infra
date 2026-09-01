// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvlink stages the ComputeDomain topology document, which gives each
// node its NVLink fabric identity.
package nvlink

import (
	"context"
	"sync/atomic"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const name = "nvlink"

// overlayRel must match the NRI plugin's defaultTopologyRelPath: the plugin
// bind-mounts the overlay root into workloads and points MOCK_TOPOLOGY_CONFIG
// at this path within it.
const overlayRel = "topology/topology.yaml"

var _ agent.Simulator = (*Simulator)(nil)

// Simulator copies the cluster topology document into the node overlay so
// NRI-injected workloads can read it.
//
// The document is cluster-wide and lists every node's domain and clique; the
// mock NVML engine selects this node's entry by NODE_NAME when it loads. That
// is why this stages the file whole rather than compiling a per-node view.
type Simulator struct {
	ready atomic.Bool
}

// New returns an nvlink Simulator.
func New() *Simulator { return &Simulator{} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage writes the topology document into the overlay. An empty document
// retracts it: the chart mounts the ConfigMap only when topology is enabled,
// and a fabric without a declared topology keeps the profile's clique defaults.
func (s *Simulator) Stage(_ context.Context, h *host.Host, state *agent.State) error {
	s.ready.Store(false)

	if len(state.TopologyRaw) == 0 {
		// The overlay is on a host mount that outlives the pod, and the NRI
		// plugin injects MOCK_TOPOLOGY_CONFIG for any container while the file
		// is there, so a retracted topology has to take the document with it.
		if err := fsutil.Remove(h.RootPath(overlayRel)); err != nil {
			return err
		}
		s.ready.Store(true)
		return nil
	}
	if err := fsutil.Write(h.RootPath(overlayRel), state.TopologyRaw, 0o644); err != nil {
		return err
	}

	s.ready.Store(true)
	return nil
}

// Discard removes the staged document. It is a no-op when Stage never
// completed successfully.
func (s *Simulator) Discard(_ context.Context, h *host.Host) error {
	if !s.ready.Load() {
		return nil
	}
	return fsutil.Remove(h.RootPath(overlayRel))
}
