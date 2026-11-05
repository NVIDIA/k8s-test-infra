// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package numatopology resolves the simulated NUMA layout from the node's real
// CPUs and memory, for the agent's NUMA enricher.
package numatopology

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cpulist"
)

// Resolve calculates one NUMA layout from the node and the declared profile.
func Resolve(h *host.Host, state *agent.State) (*agent.NUMATopology, error) {
	zones, explicit, err := rootZones(state.NodeShape.Topology.RootComplexes)
	if err != nil {
		return nil, err
	}
	cpus, err := onlineCPUs(h)
	if err != nil {
		return nil, err
	}
	assigned, err := assignCPUs(h, cpus, zones, explicit)
	if err != nil {
		return nil, err
	}
	mem, err := os.ReadFile(h.ProcPath("meminfo"))
	if err != nil {
		return nil, fmt.Errorf("read host memory: %w", err)
	}
	total, err := memoryKB(string(mem))
	if err != nil {
		return nil, err
	}
	plan := &agent.NUMATopology{Zones: make([]agent.NUMAZone, zones)}
	for i := range plan.Zones {
		plan.Zones[i] = agent.NUMAZone{ID: i, CPUs: assigned[i], MemoryKB: total / uint64(zones)}
		if uint64(i) < total%uint64(zones) {
			plan.Zones[i].MemoryKB++
		}
	}
	return plan, nil
}

func rootZones(roots []agent.RootComplex) (int, map[int]string, error) {
	if len(roots) == 0 {
		return 0, nil, errors.New("NUMA simulation requires explicit pcie_topology.root_complexes")
	}
	ids := map[int]bool{}
	explicit := map[int]string{}
	for _, rc := range roots {
		if rc.NUMANode < 0 || rc.NUMANode >= 8 {
			return 0, nil, fmt.Errorf("unsupported NUMA ID %d; use contiguous IDs starting at zero, up to eight zones", rc.NUMANode)
		}
		ids[rc.NUMANode] = true
		if rc.CPUAffinity == "" {
			continue
		}
		if old := explicit[rc.NUMANode]; old != "" && old != rc.CPUAffinity {
			return 0, nil, fmt.Errorf("conflicting CPU affinities for NUMA %d", rc.NUMANode)
		}
		explicit[rc.NUMANode] = rc.CPUAffinity
	}
	for i := range len(ids) {
		if !ids[i] {
			return 0, nil, errors.New("NUMA IDs must be contiguous starting at zero")
		}
	}
	return len(ids), explicit, nil
}

func onlineCPUs(h *host.Host) ([]int, error) {
	raw, err := os.ReadFile(h.SysPath("devices/system/cpu/online"))
	if err != nil {
		return nil, fmt.Errorf("read online CPUs: %w", err)
	}
	cpus, err := cpulist.Parse(string(raw))
	if err != nil {
		return nil, err
	}
	slices.Sort(cpus)
	return slices.Compact(cpus), nil
}

func assignCPUs(h *host.Host, cpus []int, zones int, explicit map[int]string) ([][]int, error) {
	if len(cpus) < zones {
		return nil, fmt.Errorf("%d online CPUs cannot populate %d NUMA zones", len(cpus), zones)
	}
	if len(explicit) > 0 {
		return explicitCPUs(cpus, zones, explicit)
	}
	groups, err := coreGroups(h, cpus)
	if err != nil {
		return nil, err
	}
	if len(groups) < zones {
		return nil, fmt.Errorf("%d physical cores cannot populate %d NUMA zones without splitting threads", len(groups), zones)
	}
	assigned := make([][]int, zones)
	for i, group := range groups {
		zone := i * zones / len(groups)
		assigned[zone] = append(assigned[zone], group...)
	}
	for _, list := range assigned {
		slices.Sort(list)
	}
	return assigned, nil
}

func explicitCPUs(cpus []int, zones int, explicit map[int]string) ([][]int, error) {
	if len(explicit) != zones {
		return nil, errors.New("declare cpu_affinity for every NUMA zone or leave all automatic")
	}
	assigned := make([][]int, zones)
	seen := map[int]bool{}
	for i := range assigned {
		list, err := cpulist.Parse(explicit[i])
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("NUMA %d has no CPUs", i)
		}
		for _, cpu := range list {
			if !slices.Contains(cpus, cpu) || seen[cpu] {
				return nil, fmt.Errorf("CPU %d is absent or assigned to multiple NUMA zones", cpu)
			}
			seen[cpu] = true
		}
		slices.Sort(list)
		assigned[i] = list
	}
	if len(seen) != len(cpus) {
		return nil, fmt.Errorf("CPU affinities must cover all %d online CPUs", len(cpus))
	}
	return assigned, nil
}

func coreGroups(h *host.Host, cpus []int) ([][]int, error) {
	groups := [][]int{}
	index := map[string]int{}
	for _, cpu := range cpus {
		base := h.SysPath(fmt.Sprintf("devices/system/cpu/cpu%d/topology", cpu))
		core, err := os.ReadFile(filepath.Join(base, "core_id"))
		if err != nil {
			return nil, fmt.Errorf("read CPU %d core: %w", cpu, err)
		}
		socket, err := os.ReadFile(filepath.Join(base, "physical_package_id"))
		if err != nil {
			return nil, fmt.Errorf("read CPU %d package: %w", cpu, err)
		}
		key := strings.TrimSpace(string(socket)) + ":" + strings.TrimSpace(string(core))
		at, ok := index[key]
		if !ok {
			at = len(groups)
			index[key] = at
			groups = append(groups, nil)
		}
		groups[at] = append(groups[at], cpu)
	}
	return groups, nil
}

func memoryKB(text string) (uint64, error) {
	for line := range strings.SplitSeq(text, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == "MemTotal:" {
			value, err := strconv.ParseUint(parts[1], 10, 64)
			if err == nil && value > 0 {
				return value, nil
			}
			return 0, fmt.Errorf("invalid MemTotal %q", line)
		}
	}
	return 0, errors.New("host meminfo has no MemTotal")
}
