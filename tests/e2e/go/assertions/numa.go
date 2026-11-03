// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// NodeResourceTopology is the subset published by the real NFD topology updater.
// Keeping quantities typed accepts equivalent values such as 1Gi and 1024Mi.
type NodeResourceTopology struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Attributes []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"attributes"`
	Zones []NUMAResourceZone `json:"zones"`
}

// NUMAResourceZone contains the resources associated with one NUMA node.
type NUMAResourceZone struct {
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Resources []NUMAResource `json:"resources"`
}

// NUMAResource distinguishes total, allocatable, and currently free resources.
type NUMAResource struct {
	Name        string            `json:"name"`
	Capacity    resource.Quantity `json:"capacity"`
	Allocatable resource.Quantity `json:"allocatable"`
	Available   resource.Quantity `json:"available"`
}

// NUMAExpectation comes from the test profile and the node's original inventory,
// not from Mokka's rendered plan, so a wrong plan cannot validate itself.
type NUMAExpectation struct {
	Node        string
	CPUs        int64
	MemoryBytes int64
	GPUsPerZone int64
}

var numaResourceNames = []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, "nvidia.com/gpu"}

// NUMATopologyProblems checks the two-zone A100 fixture and its host capacity.
func NUMATopologyProblems(nrt NodeResourceTopology, want NUMAExpectation) []string {
	var problems []string
	if nrt.Metadata.Name != want.Node {
		problems = append(problems, fmt.Sprintf("NRT name %q, want %q", nrt.Metadata.Name, want.Node))
	}
	if len(nrt.Zones) != 2 {
		problems = append(problems, fmt.Sprintf("got %d zones, want 2", len(nrt.Zones)))
	}
	problems = append(problems, topologyAttributeProblems(nrt)...)
	seen := map[string]bool{}
	var cpus, memory int64
	for _, zone := range nrt.Zones {
		if seen[zone.Name] || (zone.Name != "node-0" && zone.Name != "node-1") {
			problems = append(problems, "unexpected or duplicate zone "+zone.Name)
		}
		seen[zone.Name] = true
		problems = append(problems, numaZoneProblems(zone, want)...)
		resources := zoneResources(zone)
		cpu, mem := resources[corev1.ResourceCPU], resources[corev1.ResourceMemory]
		cpus += cpu.Capacity.Value()
		memory += mem.Capacity.Value()
	}
	if cpus != want.CPUs {
		problems = append(problems, fmt.Sprintf("total CPU capacity %d, want %d", cpus, want.CPUs))
	}
	if memory != want.MemoryBytes {
		problems = append(problems, fmt.Sprintf("total memory capacity %d, want %d", memory, want.MemoryBytes))
	}
	return problems
}

func topologyAttributeProblems(nrt NodeResourceTopology) []string {
	attrs := map[string]string{}
	for _, a := range nrt.Attributes {
		attrs[a.Name] = a.Value
	}
	var problems []string
	for name, want := range map[string]string{"topologyManagerPolicy": "single-numa-node", "topologyManagerScope": "container"} {
		if attrs[name] != want {
			problems = append(problems, fmt.Sprintf("%s=%q, want %q", name, attrs[name], want))
		}
	}
	return problems
}

func numaZoneProblems(zone NUMAResourceZone, want NUMAExpectation) []string {
	var problems []string
	if zone.Type != "Node" {
		problems = append(problems, "zone type must be Node: "+zone.Name)
	}
	resources := zoneResources(zone)
	for _, name := range numaResourceNames {
		res, ok := resources[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s missing %s", zone.Name, name))
			continue
		}
		problems = append(problems, resourceBoundsProblems(zone.Name, res)...)
	}
	memory := resources[corev1.ResourceMemory]
	kb := want.MemoryBytes / 1024
	perZone := kb / 2
	if zone.Name == "node-0" {
		perZone += kb % 2
	}
	if memory.Capacity.Value() != perZone*1024 {
		problems = append(problems, "incorrect memory split on "+zone.Name)
	}

	gpu := resources["nvidia.com/gpu"]
	if gpu.Capacity.Value() != want.GPUsPerZone || gpu.Allocatable.Value() != want.GPUsPerZone {
		problems = append(problems, fmt.Sprintf("%s must have %d GPUs", zone.Name, want.GPUsPerZone))
	}
	return problems
}

func resourceBoundsProblems(zone string, res NUMAResource) []string {
	if res.Capacity.Sign() <= 0 || res.Allocatable.Sign() < 0 || res.Available.Sign() < 0 || res.Available.Cmp(res.Allocatable) > 0 || res.Allocatable.Cmp(res.Capacity) > 0 {
		return []string{fmt.Sprintf("invalid capacity/allocatable/available for %s/%s", zone, res.Name)}
	}
	return nil
}

func zoneResources(zone NUMAResourceZone) map[corev1.ResourceName]NUMAResource {
	out := make(map[corev1.ResourceName]NUMAResource, len(zone.Resources))
	for _, res := range zone.Resources {
		out[corev1.ResourceName(res.Name)] = res
	}
	return out
}

func topologyZones(nrt NodeResourceTopology) map[string]NUMAResourceZone {
	out := make(map[string]NUMAResourceZone, len(nrt.Zones))
	for _, zone := range nrt.Zones {
		out[zone.Name] = zone
	}
	return out
}

// NUMAAllocationProblems requires all requested resources to decrease by the
// exact request on one zone, with capacity and allocatable values unchanged.
func NUMAAllocationProblems(before, after NodeResourceTopology, request corev1.ResourceList) []string {
	var problems []string
	zones := topologyZones(after)
	if len(after.Zones) != len(before.Zones) {
		problems = append(problems, "zone count changed")
	}
	changed := 0
	for _, old := range before.Zones {
		current, ok := zones[old.Name]
		if !ok {
			problems = append(problems, "missing zone "+old.Name)
			continue
		}
		delta, errs := numaResourceDeltas(old, current)
		problems = append(problems, errs...)
		anyChange := false
		for _, amount := range delta {
			anyChange = anyChange || !amount.IsZero()
		}
		if !anyChange {
			continue
		}
		changed++
		for name, want := range request {
			amount := delta[name]
			if amount.Cmp(want) != 0 {
				problems = append(problems, fmt.Sprintf("%s/%s allocation delta %s, want %s in one zone", old.Name, name, amount.String(), want.String()))
			}
		}
	}
	if changed != 1 {
		problems = append(problems, fmt.Sprintf("allocation must change exactly one zone; changed %d", changed))
	}
	return problems
}

// NUMAAvailabilityProblems checks that deleting the workload restores the exact
// baseline, rather than accepting a resource count that merely increased.
func NUMAAvailabilityProblems(before, after NodeResourceTopology) []string {
	var problems []string
	zones := topologyZones(after)
	if len(after.Zones) != len(before.Zones) {
		problems = append(problems, "zone count changed")
	}
	for _, old := range before.Zones {
		current, ok := zones[old.Name]
		if !ok {
			problems = append(problems, "missing zone "+old.Name)
			continue
		}
		delta, errs := numaResourceDeltas(old, current)
		problems = append(problems, errs...)
		for name, amount := range delta {
			if !amount.IsZero() {
				problems = append(problems, fmt.Sprintf("%s/%s did not return to baseline", old.Name, name))
			}
		}
	}
	return problems
}

func numaResourceDeltas(before, after NUMAResourceZone) (corev1.ResourceList, []string) {
	old, current := zoneResources(before), zoneResources(after)
	delta := corev1.ResourceList{}
	var problems []string
	for _, name := range numaResourceNames {
		a, oldOK := old[name]
		b, newOK := current[name]
		if !oldOK || !newOK {
			problems = append(problems, fmt.Sprintf("%s missing resource %s", before.Name, name))
			continue
		}
		if a.Capacity.Cmp(b.Capacity) != 0 || a.Allocatable.Cmp(b.Allocatable) != 0 {
			problems = append(problems, fmt.Sprintf("%s/%s capacity changed during allocation", before.Name, name))
		}
		amount := a.Available.DeepCopy()
		amount.Sub(b.Available)
		delta[name] = amount
	}
	return delta, problems
}
