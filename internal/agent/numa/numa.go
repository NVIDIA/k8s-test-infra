// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package numa serves the profile's NUMA nodes at /sys/bus/node/devices on a
// node whose kernel exposes none — one built without CONFIG_NUMA, as Docker
// Desktop's arm64 VM is — where NFD's memory source otherwise fails on every
// discovery pass. A node whose kernel exposes NUMA nodes is never touched.
package numa

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/sysoverlay"
)

// relPath is the kernel's NUMA node bus relative to /sys.
const relPath = "bus/node"

// New returns the simulator serving /sys/bus/node/devices.
func New(h *host.Host) *sysoverlay.Simulator {
	return sysoverlay.NewSimulator("numa", sysoverlay.At(h, relPath), stage)
}

// stage writes one entry per NUMA node into entry/devices, linked where the
// kernel links it. Only the count is simulated: the link targets under
// /sys/devices/system/node stay absent, because kubelet reads that tree for
// the node's CPU and memory topology and a fabricated one would mislead it.
func stage(entry string, state *agent.State) error {
	dir := filepath.Join(entry, "devices")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	want := make(map[string]bool)
	for i := range nodeCount(state) {
		name := "node" + strconv.Itoa(i)
		want[name] = true
		if err := fsutil.Symlink("../../../devices/system/node/"+name, filepath.Join(dir, name)); err != nil {
			return err
		}
	}

	existing, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list %s: %w", dir, err)
	}
	for _, e := range existing {
		if want[e.Name()] {
			continue
		}
		if err := fsutil.Remove(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// nodeCount is one past the highest NUMA node the profile's PCIe layout
// declares, and at least one: every Linux system has node0. The declared
// layout is used rather than the rendered one because a reduced GPU count
// drops root complexes, not the CPU sockets they hang off.
func nodeCount(state *agent.State) int {
	count := 1
	for _, rc := range state.NodeShape.Topology.RootComplexes {
		count = max(count, rc.NUMANode+1)
	}
	return count
}
