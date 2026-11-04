// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package numa stages a host-visible NUMA topology for GPU-only simulation.
package numa

import (
	"fmt"
	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cpulist"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Render owns a separate tree, outside the PCI renderer's pruning scope.
func Render(h *host.Host, plan *agent.NUMATopology) error {
	root := h.RootPath("numa/sysnode")
	write := func(name, value string) error { return fsutil.Write(filepath.Join(root, name), []byte(value), 0644) }
	if err := resetTree(root); err != nil {
		return err
	}
	nodes := make([]int, len(plan.Zones))
	for i := range nodes {
		nodes[i] = i
	}
	for _, name := range []string{"online", "possible", "has_cpu", "has_memory", "has_normal_memory", "has_high_memory"} {
		if err := write(name, cpulist.Format(nodes)+"\n"); err != nil {
			return err
		}
	}
	memoryBlocks, err := filepath.Glob(h.SysPath("devices/system/memory/memory[0-9]*"))
	if err != nil {
		return err
	}
	sort.Strings(memoryBlocks)
	for _, zone := range plan.Zones {
		if err := renderZone(root, zone, len(plan.Zones), write); err != nil {
			return err
		}
	}
	for i, block := range memoryBlocks {
		zone := i * len(plan.Zones) / len(memoryBlocks)
		if err := fsutil.Symlink("../../memory/"+filepath.Base(block), filepath.Join(root, fmt.Sprintf("node%d", zone), filepath.Base(block))); err != nil {
			return err
		}
	}
	return renderNodeBus(h, plan)
}

// NFD enumerates memory through the node bus, whose links must match the
// devices/system/node view used by kubelet and hardware topology readers.
func renderNodeBus(h *host.Host, plan *agent.NUMATopology) error {
	root := h.RootPath("numa/sysbusnode")
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	for _, zone := range plan.Zones {
		name := fmt.Sprintf("node%d", zone.ID)
		if err := fsutil.Symlink("../../../devices/system/node/"+name, filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}

// Render runs only before the tree is mounted. Remove old zones after an explicit restore.
func resetTree(root string) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return fsutil.Write(filepath.Join(root, ".mokka-numa"), []byte("mokka-numa-v1\n"), 0644)
}

func renderZone(root string, zone agent.NUMAZone, zones int, write func(string, string) error) error {
	dir := fmt.Sprintf("node%d", zone.ID)
	if err := os.MkdirAll(filepath.Join(root, dir, "hugepages"), 0755); err != nil {
		return err
	}
	for name, value := range map[string]string{"cpulist": cpulist.Format(zone.CPUs) + "\n", "cpumap": cpuMask(zone.CPUs) + "\n", "meminfo": fmt.Sprintf("Node %d MemTotal: %d kB\nNode %d MemFree: %d kB\nNode %d MemUsed: 0 kB\n", zone.ID, zone.MemoryKB, zone.ID, zone.MemoryKB, zone.ID)} {
		if err := write(filepath.Join(dir, name), value); err != nil {
			return err
		}
	}
	distances := make([]string, zones)
	for i := range distances {
		distances[i] = "21"
		if i == zone.ID {
			distances[i] = "10"
		}
	}
	if err := write(filepath.Join(dir, "distance"), strings.Join(distances, " ")+"\n"); err != nil {
		return err
	}
	for _, cpu := range zone.CPUs {
		if err := fsutil.Symlink(fmt.Sprintf("../../cpu/cpu%d", cpu), filepath.Join(root, dir, fmt.Sprintf("cpu%d", cpu))); err != nil {
			return err
		}
	}
	return nil
}

func cpuMask(cpus []int) string {
	words := make([]uint32, cpus[len(cpus)-1]/32+1)
	for _, cpu := range cpus {
		words[cpu/32] |= 1 << uint(cpu%32)
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[len(words)-1-i] = fmt.Sprintf("%08x", w)
	}
	return strings.Join(parts, ",")
}
