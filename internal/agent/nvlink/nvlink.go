// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvlink stages the ComputeDomain topology document, which gives each
// node its NVLink fabric identity.
package nvlink

import (
	"context"
	"errors"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

const name = "nvlink"

// overlayRel must match the NRI plugin's defaultTopologyRelPath: the plugin
// bind-mounts the overlay root into workloads and points MOCK_TOPOLOGY_CONFIG
// at this path within it.
const overlayRel = "topology/topology.yaml"

// driverRel puts the same document beside the driver tree's config.yaml,
// where the mock NVML library looks when nothing points it elsewhere. That is
// how a process that only mounts the driver root, such as the DRA driver's
// kubelet plugin, sees the node's clique.
const driverRel = "driver/config/topology.yaml"

var stagedPaths = []string{overlayRel, driverRel}

var _ agent.Simulator = (*Simulator)(nil)

// Simulator copies the cluster topology document into the node overlay so
// NRI-injected workloads can read it.
//
// The document is cluster-wide and lists every node's domain and clique; the
// mock NVML engine selects this node's entry by NODE_NAME when it loads. That
// is why this stages the file whole rather than compiling a per-node view.
type Simulator struct {
	host  *host.Host
	ready atomic.Bool
}

// New returns an nvlink Simulator.
func New(h *host.Host) *Simulator { return &Simulator{host: h} }

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether the last Stage call completed without error.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage writes the topology document into the overlay. An empty document
// retracts it: the chart mounts the ConfigMap only when topology is enabled,
// and a fabric without a declared topology keeps the profile's clique defaults.
func (s *Simulator) Stage(_ context.Context, state *agent.State) error {
	s.ready.Store(false)
	zap.L().Info("staging simulator", zap.String("simulator", name))

	if len(state.TopologyRaw) == 0 {
		// The overlay is on a host mount that outlives the pod, and the NRI
		// plugin injects MOCK_TOPOLOGY_CONFIG for any container while the file
		// is there, so a retracted topology has to take the document with it.
		if err := s.remove(); err != nil {
			return err
		}

		s.ready.Store(true)
		zap.L().Info("simulator staged; no topology document", zap.String("simulator", name))
		return nil
	}

	for _, rel := range stagedPaths {
		if err := fsutil.Write(s.host.RootPath(rel), state.TopologyRaw, 0o644); err != nil {
			return err
		}
	}

	s.ready.Store(true)
	zap.L().Info("simulator staged", zap.String("simulator", name))
	return nil
}

// Discard removes the staged document.
func (s *Simulator) Discard(_ context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))
	return s.remove()
}

func (s *Simulator) remove() error {
	var errs []error
	for _, rel := range stagedPaths {
		if err := fsutil.Remove(s.host.RootPath(rel)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
