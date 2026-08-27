// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// twoRootState is a gb300-shaped node: two host bridges, two GPUs each.
func twoRootState() *State {
	return &State{
		NodeShape: NodeShape{Topology: PCIeTopology{RootComplexes: []RootComplex{
			{ID: "pci0000:00", NUMANode: 0, DeviceBDFs: []string{"0000:0a:00.0", "0000:0b:00.0"}},
			{ID: "pci0000:40", NUMANode: 1, DeviceBDFs: []string{"0000:4a:00.0", "0000:4b:00.0"}},
		}}},
		Devices: []DeviceSpec{
			{Index: 0, PCIBusID: "0000:0A:00.0"},
			{Index: 1, PCIBusID: "0000:0B:00.0"},
			{Index: 2, PCIBusID: "0000:4A:00.0"},
			{Index: 3, PCIBusID: "0000:4B:00.0"},
		},
	}
}

func bdfsOf(rcs []RootComplex) map[string][]string {
	out := make(map[string][]string, len(rcs))
	for _, rc := range rcs {
		out[rc.ID] = rc.DeviceBDFs
	}
	return out
}

func TestPCITopology_KeepsADeclaredLayout(t *testing.T) {
	rcs := twoRootState().PCITopology()

	require.Equal(t, map[string][]string{
		"pci0000:00": {"0000:0a:00.0", "0000:0b:00.0"},
		"pci0000:40": {"0000:4a:00.0", "0000:4b:00.0"},
	}, bdfsOf(rcs))
}

// GPU_COUNT truncates State.Devices while pcie_topology is copied from the
// profile whole, so the profile's BDFs outlive the GPUs NVML reports. Rendering
// them would put phantom NVIDIA 3D controllers in the tree now served at the
// kernel paths — every attribute plausible, no NVML device behind them.
func TestPCITopology_DropsBDFsNoDeviceClaims(t *testing.T) {
	state := twoRootState()
	state.Devices = state.Devices[:2]

	rcs := state.PCITopology()

	require.Equal(t, map[string][]string{
		"pci0000:00": {"0000:0a:00.0", "0000:0b:00.0"},
	}, bdfsOf(rcs), "the emptied root goes with its devices")
}

// The mirror case: a hand-written pcie_topology that forgets a device. Coverage
// beats placement here — an unplaced GPU is one a consumer cannot resolve at
// all, where a misplaced one only reports the wrong pcieRoot.
func TestPCITopology_AdoptsADeviceNoRootClaims(t *testing.T) {
	state := twoRootState()
	state.Devices = append(state.Devices, DeviceSpec{Index: 4, PCIBusID: "0000:8a:00.0"})

	rcs := state.PCITopology()

	require.Equal(t, []string{"0000:0a:00.0", "0000:0b:00.0", "0000:8a:00.0"},
		bdfsOf(rcs)["pci0000:00"], "adopted by the first declared root")
}

func TestPCITopology_SynthesizesARootWhenNoneDeclared(t *testing.T) {
	state := &State{Devices: []DeviceSpec{
		{Index: 0, PCIBusID: "0000:1A:00.0"},
		{Index: 1},
		{Index: 2, PCIBusID: "0000:1B:00.0"},
	}}

	rcs := state.PCITopology()

	require.Len(t, rcs, 1)
	require.Equal(t, DefaultRootComplexID, rcs[0].ID)
	require.Equal(t, 0, rcs[0].NUMANode)
	require.Equal(t, []string{"0000:1a:00.0", "0000:1b:00.0"}, rcs[0].DeviceBDFs,
		"lowercased, and the device without a BDF is skipped")
}

func TestPCITopology_NilWithoutADeviceBDF(t *testing.T) {
	require.Nil(t, (&State{}).PCITopology())
	require.Nil(t, (&State{Devices: []DeviceSpec{{Index: 0}}}).PCITopology())
	require.Nil(t, (&State{NodeShape: NodeShape{Topology: PCIeTopology{
		RootComplexes: []RootComplex{{ID: "pci0000:00", DeviceBDFs: []string{"0000:0a:00.0"}}},
	}}}).PCITopology(), "a topology no device backs renders nothing")
}

// The CDI simulator gates the sysfs mounts it publishes on the predicate while
// pcibus renders from PCITopology, so the two must not be able to disagree: a
// false positive publishes a bind mount over /sys/devices for an empty tree.
func TestHasPCITopology_TracksPCITopology(t *testing.T) {
	noBackingDevice := &State{NodeShape: NodeShape{Topology: PCIeTopology{
		RootComplexes: []RootComplex{{ID: "pci0000:00", DeviceBDFs: []string{"0000:0a:00.0"}}},
	}}}
	truncated := twoRootState()
	truncated.Devices = truncated.Devices[:1]

	for name, state := range map[string]*State{
		"empty":              {},
		"device without bdf": {Devices: []DeviceSpec{{Index: 0}}},
		"device with bdf":    {Devices: []DeviceSpec{{Index: 0, PCIBusID: "0000:07:00.0"}}},
		"declared layout":    twoRootState(),
		"truncated by count": truncated,
		"no backing device":  noBackingDevice,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, len(state.PCITopology()) > 0, state.HasPCITopology())
		})
	}
}
