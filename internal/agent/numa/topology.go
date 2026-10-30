// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package numa stages a host-visible NUMA topology for GPU-only simulation.
package numa

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/cpulist"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"sigs.k8s.io/yaml"
)

// Zone describes one simulated NUMA node using real online CPU IDs.
type Zone struct {
	ID       int    `json:"id"`
	CPUs     []int  `json:"cpus"`
	MemoryKB uint64 `json:"memoryKB"`
}

// Plan is the resolved topology shared with NVML and PCI sysfs.
type Plan struct {
	Zones []Zone `json:"zones"`
}

// Resolve runs before parallel staging, keeping NVML and both sysfs views consistent.
func Resolve(h *host.Host, state *agent.State) (*Plan, error) {
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
	plan := &Plan{Zones: make([]Zone, zones)}
	for i := range plan.Zones {
		plan.Zones[i] = Zone{ID: i, CPUs: assigned[i], MemoryKB: total / uint64(zones)}
		if uint64(i) < total%uint64(zones) {
			plan.Zones[i].MemoryKB++
		}
	}
	if err := shareAffinities(state, plan); err != nil {
		return nil, err
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

func shareAffinities(state *agent.State, plan *Plan) error {
	state.NodeShape.Topology.RootComplexes = slices.Clone(state.NodeShape.Topology.RootComplexes)
	affinities := make(map[string]string)
	for i := range state.NodeShape.Topology.RootComplexes {
		rc := &state.NodeShape.Topology.RootComplexes[i]
		rc.CPUAffinity = cpulist.Format(plan.Zones[rc.NUMANode].CPUs)
		affinities[rc.ID] = rc.CPUAffinity
	}
	var doc map[string]any
	if err := yaml.Unmarshal(state.ConfigRaw, &doc); err != nil {
		return fmt.Errorf("read profile for NUMA affinities: %w", err)
	}
	if err := setProfileAffinities(doc, affinities); err != nil {
		return err
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("write resolved NUMA profile: %w", err)
	}
	state.ConfigRaw = raw
	return nil
}

func setProfileAffinities(doc map[string]any, affinities map[string]string) error {
	pci, ok := doc["pcie_topology"].(map[string]any)
	if !ok {
		return errors.New("profile has no pcie_topology")
	}
	list, ok := pci["root_complexes"].([]any)
	if !ok {
		return errors.New("profile has no root_complexes")
	}
	for _, entry := range list {
		rc, ok := entry.(map[string]any)
		if !ok {
			return errors.New("invalid root complex")
		}
		id, _ := rc["id"].(string)
		if value, found := affinities[id]; found {
			rc["cpu_affinity"] = value
		}
	}
	return nil
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

// Render owns a separate tree, outside the PCI renderer's pruning scope.
func Render(h *host.Host, plan *Plan) error {
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
	return nil
}

// Render runs only before the tree is mounted. Remove old zones after an explicit restore.
func resetTree(root string) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return fsutil.Write(filepath.Join(root, ".mokka-numa"), []byte("mokka-numa-v1\n"), 0644)
}

func renderZone(root string, zone Zone, zones int, write func(string, string) error) error {
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
