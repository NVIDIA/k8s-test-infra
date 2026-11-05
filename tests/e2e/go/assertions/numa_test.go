// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func nrtFixture(t *testing.T) NodeResourceTopology {
	t.Helper()
	var nrt NodeResourceTopology
	require.NoError(t, json.Unmarshal([]byte(`{
 "metadata":{"name":"worker-0"},
 "attributes":[{"name":"topologyManagerPolicy","value":"single-numa-node"},{"name":"topologyManagerScope","value":"container"}],
 "zones":[
  {"name":"node-0","type":"Node","resources":[
   {"name":"cpu","capacity":"2","allocatable":"1","available":"1"},
   {"name":"memory","capacity":"1Gi","allocatable":"900Mi","available":"800Mi"},
   {"name":"nvidia.com/gpu","capacity":"4","allocatable":"4","available":"4"}]},
  {"name":"node-1","type":"Node","resources":[
   {"name":"cpu","capacity":"2","allocatable":"2","available":"2"},
   {"name":"memory","capacity":"1Gi","allocatable":"900Mi","available":"800Mi"},
   {"name":"nvidia.com/gpu","capacity":"4","allocatable":"4","available":"4"}]}]
 }`), &nrt))
	return nrt
}

func TestNUMATopologyChecksContents(t *testing.T) {
	t.Parallel()
	want := NUMAExpectation{Node: "worker-0", CPUs: 4, MemoryBytes: 2 * 1024 * 1024 * 1024, GPUsPerZone: 4}
	require.Empty(t, NUMATopologyProblems(nrtFixture(t), want))
	for _, tc := range []struct {
		name    string
		corrupt func(*NodeResourceTopology)
	}{
		{"wrong node", func(n *NodeResourceTopology) { n.Metadata.Name = "control-plane" }},
		{"missing zone", func(n *NodeResourceTopology) { n.Zones = n.Zones[:1] }},
		{"duplicate zone", func(n *NodeResourceTopology) { n.Zones[1].Name = "node-0" }},
		{"wrong policy", func(n *NodeResourceTopology) { n.Attributes[0].Value = "none" }},
		{"missing memory", func(n *NodeResourceTopology) {
			n.Zones[0].Resources = append(n.Zones[0].Resources[:1], n.Zones[0].Resources[2:]...)
		}},
		{"wrong GPU placement", func(n *NodeResourceTopology) { n.Zones[0].Resources[2].Capacity = resource.MustParse("8") }},
		{"wrong CPU capacity", func(n *NodeResourceTopology) { n.Zones[0].Resources[0].Capacity = resource.MustParse("3") }},
		{"wrong memory capacity", func(n *NodeResourceTopology) { n.Zones[0].Resources[1].Capacity = resource.MustParse("2Gi") }},
		{"invalid availability", func(n *NodeResourceTopology) { n.Zones[0].Resources[0].Available = resource.MustParse("2") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			nrt := nrtFixture(t)
			tc.corrupt(&nrt)
			require.NotEmpty(t, NUMATopologyProblems(nrt, want))
		})
	}
}

func TestNUMAAllocationRequiresOneMatchingZone(t *testing.T) {
	t.Parallel()
	request := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("64Mi"), "nvidia.com/gpu": resource.MustParse("1")}
	before := nrtFixture(t)
	allocated := nrtFixture(t)
	allocated.Zones[1].Resources[0].Available = resource.MustParse("1")
	allocated.Zones[1].Resources[1].Available = resource.MustParse("736Mi")
	allocated.Zones[1].Resources[2].Available = resource.MustParse("3")
	require.Empty(t, NUMAAllocationProblems(before, allocated, request))
	require.NotEmpty(t, NUMAAllocationProblems(before, before, request), "unchanged NRT must not pass")
	split := nrtFixture(t)
	split.Zones[0].Resources[0].Available = resource.MustParse("0")
	split.Zones[1].Resources[1].Available = resource.MustParse("736Mi")
	split.Zones[1].Resources[2].Available = resource.MustParse("3")
	require.Contains(t, strings.Join(NUMAAllocationProblems(before, split, request), " "), "one zone")
	require.NotEmpty(t, NUMAAvailabilityProblems(before, allocated))
	require.Empty(t, NUMAAvailabilityProblems(before, nrtFixture(t)))
}
