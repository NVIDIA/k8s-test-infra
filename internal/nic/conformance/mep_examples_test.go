// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package conformance

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
)

var yamlBlock = regexp.MustCompile("(?s)```yaml\n(.*?)```")

// TestMEPExamples checks the NIC examples in a MEP draft against what Mokka
// ships: every SNICProfile example must validate and, when it shares a name
// with a shipped profile, equal it; a rack example's NIC slots, root ports,
// GPU slots and RDMA software must equal the rack fixture of the same name;
// and an inventory example's configuration choices for a rack fixture must
// equal its inventory fixture's. Set MEP_README to the MEP's README.md to run
// it.
func TestMEPExamples(t *testing.T) {
	readme := os.Getenv("MEP_README")
	if readme == "" {
		t.Skip("MEP_README not set")
	}
	data, err := os.ReadFile(readme)
	require.NoError(t, err)

	shipped := load(t, vrNVL72).profiles
	racks := map[string]*v1alpha1.SGPURackProfile{}
	choices := map[string][]v1alpha1.NICConfigurationChoice{}
	for _, fx := range fixtures {
		d := load(t, fx)
		racks[d.rack.Name] = d.rack
		if d.choices != nil {
			choices[d.rack.Name] = d.choices
		}
	}

	checked := 0
	for _, m := range yamlBlock.FindAllStringSubmatch(string(data), -1) {
		switch block := m[1]; {
		case strings.HasPrefix(block, "kind: SNICProfile\n"):
			checkMEPProfile(t, block, shipped)
			checked++
		case strings.HasPrefix(block, "kind: SGPURackProfile\n"):
			if checkMEPRack(t, block, racks) {
				checked++
			}
		case strings.HasPrefix(block, "kind: SGPUInventory\n"):
			checked += checkMEPInventory(t, block, choices)
		}
	}
	require.Positive(t, checked, "no NIC examples found in %s", readme)
	t.Logf("checked %d NIC examples", checked)
}

func checkMEPProfile(t *testing.T, block string, shipped declaration.Profiles) {
	t.Helper()
	var p v1alpha1.SNICProfile
	require.NoError(t, yaml.UnmarshalStrict([]byte(block), &p), block)
	require.NoError(t, declaration.ValidateProfile(&p), p.Name)
	if s, ok := shipped[p.Name]; ok {
		require.Equal(t, s.Spec, p.Spec, "MEP example %s differs from the shipped profile", p.Name)
	}
}

// checkMEPRack reports whether the block is a rack example the fixtures
// cover, and checks it if so.
func checkMEPRack(t *testing.T, block string, racks map[string]*v1alpha1.SGPURackProfile) bool {
	t.Helper()
	var named struct {
		Metadata struct{ Name string } `json:"metadata"`
	}
	if yaml.Unmarshal([]byte(block), &named) != nil || racks[named.Metadata.Name] == nil {
		return false // a schema sketch, or a rack no fixture covers
	}
	// A sample named after a fixture must decode: a silent skip would hide it.
	var r v1alpha1.SGPURackProfile
	require.NoError(t, yaml.UnmarshalStrict([]byte(block), &r), "MEP example %s", named.Metadata.Name)
	fixture := racks[r.Name]
	require.Equal(t, fixture.Spec.Software.RDMA, r.Spec.Software.RDMA, "MEP example %s: software.rdma", r.Name)
	require.Equal(t, fixture.Spec.Node.Topology.RootPorts, r.Spec.Node.Topology.RootPorts, "MEP example %s: rootPorts", r.Name)
	require.Equal(t, fixture.Spec.Node.Topology.GPUSlots, r.Spec.Node.Topology.GPUSlots, "MEP example %s: gpuSlots", r.Name)
	slots := map[string]v1alpha1.NICSlot{}
	for _, s := range fixture.Spec.Node.Topology.NICs {
		slots[s.Slot] = s
	}
	for _, s := range r.Spec.Node.Topology.NICs {
		require.Equal(t, slots[s.Slot], s, "MEP example %s: slot %s differs from the fixture", r.Name, s.Slot)
	}
	return true
}

// checkMEPInventory checks an inventory example's configuration choices for
// each rack that has an inventory fixture, and returns how many it checked.
func checkMEPInventory(t *testing.T, block string, choices map[string][]v1alpha1.NICConfigurationChoice) int {
	t.Helper()
	var inv v1alpha1.SGPUInventory
	if yaml.UnmarshalStrict([]byte(block), &inv) != nil {
		return 0 // a schema sketch
	}
	n := 0
	for _, g := range inv.Spec.RackGroups {
		want, ok := choices[g.ProfileRef.Name]
		if !ok {
			continue
		}
		require.Equal(t, want, g.NICConfigurations, "MEP example: rack group %s's nicConfigurations", g.ID)
		n++
	}
	return n
}
