// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// devWithBDF builds a DeviceOverride carrying just a PCI bus id.
func devWithBDF(index int, bdf string) DeviceOverride {
	return DeviceOverride{
		Index:        index,
		DeviceConfig: DeviceConfig{PCI: &PCIConfig{BusID: bdf}},
	}
}

func switchLinks(n int, bdf string) []NVLinkLinkConfig {
	links := make([]NVLinkLinkConfig, n)
	for i := 0; i < n; i++ {
		links[i] = NVLinkLinkConfig{Link: i, State: "active", RemoteDeviceType: "switch", RemotePCIBusID: bdf}
	}
	return links
}

func intptr(i int) *int { return &i }

// TestNodeFabric_SwitchTraversalNV18 builds a GB200-style 2-GPU slice where
// every GPU has 18 links to a shared NVSwitch and asserts the derived NV#
// count between the pair is 18 (NV18).
func TestNodeFabric_SwitchTraversalNV18(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.0", NumDevices: 2},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:0B:00.0"),
		},
		NVLink: &NVLinkConfig{
			Version:              5,
			LinksPerGPU:          18,
			BandwidthPerLinkGBPS: 100,
			Switches:             []NVSwitchConfig{{BDF: "0000:0F:00.0", UUID: "sw-0"}},
			Defaults:             &NVLinkDefaults{State: "active", DutyCycle: 0.05},
			DeviceLinks: []DeviceLinksConfig{
				{Index: 0, Links: switchLinks(18, "0000:0F:00.0")},
				{Index: 1, Links: switchLinks(18, "0000:0F:00.0")},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 2, YAMLConfig: yc})

	got := f.NVLinkCount(0, 1)
	require.Equal(t, 18, got, "NVLinkCount(0,1): got %d, want 18 (NV18)", got)
	got = f.NVLinkCount(1, 0)
	require.Equal(t, 18, got, "NVLinkCount(1,0): got %d, want 18", got)
	got = f.NVLinkCount(0, 0)
	require.Equal(t, 0, got, "NVLinkCount(0,0) diagonal: got %d, want 0", got)
	require.Len(t, f.Switches(), 1, "Switches: got %d, want 1", len(f.Switches()))
	// Remote device type of a switch link must be SWITCH.
	l, ok := f.Link(0, 0)
	require.True(t, ok, "link 0 remote kind: got %v ok=%v, want RemoteSwitch", l.RemoteKind, ok)
	require.Equal(t, RemoteSwitch, l.RemoteKind, "link 0 remote kind: got %v ok=%v, want RemoteSwitch", l.RemoteKind, ok)
	require.Empty(t, f.Validate(), "unexpected warnings: %v", f.Validate())
}

// TestNodeFabric_SwitchLinkAutoExpansion verifies that a profile declaring
// NVSwitches + links_per_gpu, but no explicit per-device links, has its
// links auto-expanded so every GPU pair reports NV{links_per_gpu} — the
// mechanism that gives the shipped HGX/GB200 profiles a full NV18 matrix
// without hand-authoring N*18 YAML entries.
func TestNodeFabric_SwitchLinkAutoExpansion(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.0", NumDevices: 4},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:0B:00.0"),
			devWithBDF(2, "0000:4A:00.0"),
			devWithBDF(3, "0000:4B:00.0"),
		},
		NVLink: &NVLinkConfig{
			Version:              5,
			LinksPerGPU:          18,
			BandwidthPerLinkGBPS: 100,
			Switches: []NVSwitchConfig{
				{BDF: "0000:01:00.0", UUID: "sw-0"},
				{BDF: "0000:02:00.0", UUID: "sw-1"},
			},
			Defaults: &NVLinkDefaults{State: "active", DutyCycle: 0.05},
			// No DeviceLinks and no legacy Links: expansion must fill them.
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 4, YAMLConfig: yc})

	for i := 0; i < 4; i++ {
		got := f.NumLinks(i)
		require.Equal(t, 18, got, "device %d auto-expanded links: got %d, want 18", i, got)
		for j := 0; j < 4; j++ {
			want := 18
			if i == j {
				want = 0
			}
			got := f.NVLinkCount(i, j)
			require.Equal(t, want, got, "NVLinkCount(%d,%d): got %d, want %d", i, j, got, want)
		}
	}
	// Every expanded link must be a switch endpoint, fanned round-robin.
	l0, _ := f.Link(0, 0)
	l1, _ := f.Link(0, 1)
	require.Equal(t, RemoteSwitch, l0.RemoteKind, "expanded links must be RemoteSwitch, got %v/%v", l0.RemoteKind, l1.RemoteKind)
	require.Equal(t, RemoteSwitch, l1.RemoteKind, "expanded links must be RemoteSwitch, got %v/%v", l0.RemoteKind, l1.RemoteKind)
	require.NotEqual(t, l0.RemoteBDF, l1.RemoteBDF, "round-robin fan: links 0 and 1 should target different switches, both %q", l0.RemoteBDF)
	require.Empty(t, f.Validate(), "unexpected warnings: %v", f.Validate())
}

// TestNodeFabric_ExplicitLinksSuppressExpansion verifies a device with an
// explicit link list is left untouched by auto-expansion.
func TestNodeFabric_ExplicitLinksSuppressExpansion(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.0", NumDevices: 2},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:0B:00.0"),
		},
		NVLink: &NVLinkConfig{
			Version:     5,
			LinksPerGPU: 18,
			Switches:    []NVSwitchConfig{{BDF: "0000:01:00.0"}},
			DeviceLinks: []DeviceLinksConfig{
				{Index: 0, Links: switchLinks(4, "0000:01:00.0")},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 2, YAMLConfig: yc})

	got := f.NumLinks(0)
	require.Equal(t, 4, got, "device 0 explicit links: got %d, want 4 (not expanded)", got)
	got = f.NumLinks(1)
	require.Equal(t, 18, got, "device 1 auto-expanded links: got %d, want 18", got)
}

// TestNodeFabric_DirectGPULinks exercises direct GPU-to-GPU adjacency via
// remote_index, with a mix of links to different peers.
func TestNodeFabric_DirectGPULinks(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "550.0", NumDevices: 3},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:0B:00.0"),
			devWithBDF(2, "0000:0C:00.0"),
		},
		NVLink: &NVLinkConfig{
			Version: 4,
			DeviceLinks: []DeviceLinksConfig{
				{Index: 0, Links: []NVLinkLinkConfig{
					{Link: 0, State: "active", RemoteIndex: intptr(1)},
					{Link: 1, State: "active", RemoteIndex: intptr(1)},
					{Link: 2, State: "active", RemoteIndex: intptr(2)},
				}},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 3, YAMLConfig: yc})

	got := f.NVLinkCount(0, 1)
	require.Equal(t, 2, got, "NVLinkCount(0,1): got %d, want 2", got)
	got = f.NVLinkCount(0, 2)
	require.Equal(t, 1, got, "NVLinkCount(0,2): got %d, want 1", got)
	// Peer BDF backfilled from remote_index.
	l, _ := f.Link(0, 0)
	require.Equal(t, 1, l.RemotePeer, "link0 resolved peer=%d bdf=%q, want 1/0000:0b:00.0", l.RemotePeer, l.RemoteBDF)
	require.Equal(t, "0000:0b:00.0", l.RemoteBDF, "link0 resolved peer=%d bdf=%q, want 1/0000:0b:00.0", l.RemotePeer, l.RemoteBDF)
}

// TestNodeFabric_PairwiseTopoLevel verifies the pairwise PCIe levels:
// same root complex => SINGLE, different root same NUMA => HOSTBRIDGE,
// different NUMA => SYSTEM, diagonal => INTERNAL.
func TestNodeFabric_PairwiseTopoLevel(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.0", NumDevices: 4},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:0B:00.0"),
			devWithBDF(2, "0000:1A:00.0"),
			devWithBDF(3, "0000:8A:00.0"),
		},
		PCIeTopology: &PCIeTopologyConfig{
			CoresPerNUMA: 8,
			RootComplexes: []RootComplexConfig{
				{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:0A:00.0", "0000:0B:00.0"}},
				{ID: "pci0000:10", NUMANode: 0, Devices: []string{"0000:1A:00.0"}},
				{ID: "pci0000:80", NUMANode: 1, Devices: []string{"0000:8A:00.0"}},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 4, YAMLConfig: yc})

	cases := []struct {
		a, b int
		want nvml.GpuTopologyLevel
	}{
		{0, 0, nvml.TOPOLOGY_INTERNAL},
		{0, 1, nvml.TOPOLOGY_SINGLE},     // same root complex
		{0, 2, nvml.TOPOLOGY_HOSTBRIDGE}, // diff root, same NUMA
		{0, 3, nvml.TOPOLOGY_SYSTEM},     // diff NUMA
	}
	for _, c := range cases {
		got := f.TopoLevel(c.a, c.b)
		require.Equal(t, c.want, got, "TopoLevel(%d,%d): got %d, want %d", c.a, c.b, got, c.want)
	}
}

// TestNodeFabric_Affinity checks NUMA node + CPU mask synthesis from both
// cores_per_numa and an explicit cpu_affinity range.
func TestNodeFabric_Affinity(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.0", NumDevices: 3},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:8A:00.0"),
			devWithBDF(2, "0000:C0:00.0"),
		},
		PCIeTopology: &PCIeTopologyConfig{
			CoresPerNUMA: 8,
			RootComplexes: []RootComplexConfig{
				{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:0A:00.0"}},
				{ID: "pci0000:80", NUMANode: 1, Devices: []string{"0000:8A:00.0"}},
				{ID: "pci0000:c0", NUMANode: 2, Devices: []string{"0000:C0:00.0"}, CPUAffinity: "100-103"},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 3, YAMLConfig: yc})

	require.Equal(t, 0, f.NumaNode(0), "numa nodes: got %d/%d/%d, want 0/1/2", f.NumaNode(0), f.NumaNode(1), f.NumaNode(2))
	require.Equal(t, 1, f.NumaNode(1), "numa nodes: got %d/%d/%d, want 0/1/2", f.NumaNode(0), f.NumaNode(1), f.NumaNode(2))
	require.Equal(t, 2, f.NumaNode(2), "numa nodes: got %d/%d/%d, want 0/1/2", f.NumaNode(0), f.NumaNode(1), f.NumaNode(2))
	// device 0: synthesized cpus 0..7 -> low word bits 0..7 = 0xFF.
	mask0 := f.CPUAffinityMask(0, 2)
	require.Equal(t, uint64(0xFF), mask0[0], "dev0 cpu mask word0: got 0x%x, want 0xFF", mask0[0])
	// device 1: numa 1 -> cpus 8..15 -> bits 8..15 = 0xFF00.
	mask1 := f.CPUAffinityMask(1, 2)
	require.Equal(t, uint64(0xFF00), mask1[0], "dev1 cpu mask word0: got 0x%x, want 0xFF00", mask1[0])
	// device 2: explicit range 100-103 -> bits 100..103 in word 1.
	mask2 := f.CPUAffinityMask(2, 2)
	wantWord1 := uint64(0xF) << (100 - 64)
	require.Equal(t, wantWord1, mask2[1], "dev2 cpu mask word1: got 0x%x, want 0x%x", mask2[1], wantWord1)
	// memory affinity sets the device's NUMA node bit.
	mem1 := f.MemoryAffinityMask(1, 1)
	require.Equal(t, uint64(1)<<1, mem1[0], "dev1 memory mask: got 0x%x, want 0x2", mem1[0])
}

// TestNodeFabric_LegacyFlatLinksMapToDevice0 verifies the legacy flat
// nvlink.links list is attributed to device index 0 only (back-compat).
func TestNodeFabric_LegacyFlatLinksMapToDevice0(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "550.0", NumDevices: 2},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:0B:00.0"),
		},
		NVLink: &NVLinkConfig{
			LinksPerGPU: 6,
			Links: []NVLinkLinkConfig{
				{Link: 0, State: "active", RemoteDeviceType: "gpu", RemotePCIBusID: "0000:0B:00.0"},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 2, YAMLConfig: yc})

	require.Equal(t, 1, f.NumLinks(0), "device 0 links: got %d, want 1", f.NumLinks(0))
	require.Equal(t, 0, f.NumLinks(1), "device 1 links: got %d, want 0 (legacy maps to device 0 only)", f.NumLinks(1))
	got := f.NVLinkCount(0, 1)
	require.Equal(t, 1, got, "NVLinkCount(0,1): got %d, want 1", got)
	got = f.NVLinkCount(1, 0)
	require.Equal(t, 0, got, "NVLinkCount(1,0): got %d, want 0", got)
}

// TestNodeFabric_UnresolvedRemoteWarns asserts the load-time validator
// flags an NVLink remote BDF that resolves to no known device/switch.
func TestNodeFabric_UnresolvedRemoteWarns(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "550.0", NumDevices: 1},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
		},
		NVLink: &NVLinkConfig{
			Links: []NVLinkLinkConfig{
				{Link: 0, State: "active", RemoteDeviceType: "gpu", RemotePCIBusID: "0000:DE:AD.0"},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 1, YAMLConfig: yc})
	require.NotEmpty(t, f.Validate(), "expected a warning for unresolved remote BDF, got none")
}

// TestNodeFabric_BuiltinGB200ProfileClean asserts the shipped GB200 config
// has only resolvable NVLink endpoints (decision D-b: warn-and-continue at
// runtime, but built-in profiles must be clean).
func TestNodeFabric_BuiltinGB200ProfileClean(t *testing.T) {
	path := filepath.Join("..", "configs", "mock-nvml-config-gb200.yaml")
	yc, err := LoadYAMLConfig(path)
	require.NoError(t, err, "loading built-in gb200 profile")
	n := len(yc.Devices)
	if yc.System.NumDevices > 0 {
		n = yc.System.NumDevices
	}
	f := BuildNodeFabric(&Config{NumDevices: n, YAMLConfig: yc})
	w := f.Validate()
	require.Empty(t, w, "built-in gb200 profile has unresolved NVLink endpoints: %v", w)
}

// TestNodeFabric_BuiltinProfiles asserts every shipped profile that models
// an NVLink fabric resolves cleanly (decision D-b) and produces the expected
// NV# matrix, and that the standalone B200 negative-control profile exposes
// no NVLink fabric at all.
func TestNodeFabric_BuiltinProfiles(t *testing.T) {
	cases := []struct {
		profile    string
		wantNV     int // expected NVLinkCount between GPU 0 and GPU 1
		wantSwitch bool
	}{
		{"gb200", 18, true}, // NVLink5, 4 NVSwitches -> NV18 all-to-all
		{"gb300", 18, true},
		{"h100", 18, true}, // HGX H100, 4 NVSwitches -> NV18
		{"a100", 12, true}, // DGX A100, 6 NVSwitches -> NV12
		{"b200", 0, false}, // standalone: negative control, no NVLink fabric
	}
	for _, c := range cases {
		t.Run(c.profile, func(t *testing.T) {
			path := filepath.Join("..", "configs", "mock-nvml-config-"+c.profile+".yaml")
			yc, err := LoadYAMLConfig(path)
			require.NoError(t, err, "loading built-in %s profile", c.profile)
			n := len(yc.Devices)
			if yc.System.NumDevices > 0 {
				n = yc.System.NumDevices
			}
			f := BuildNodeFabric(&Config{NumDevices: n, YAMLConfig: yc})

			w := f.Validate()
			require.Empty(t, w, "%s: unresolved NVLink endpoints: %v", c.profile, w)
			got := len(f.Switches()) > 0
			require.Equal(t, c.wantSwitch, got, "%s: hasSwitches = %v, want %v", c.profile, got, c.wantSwitch)
			// Assert the FULL NV# matrix, not just the (0,1) cell: every
			// off-diagonal pair must equal wantNV and every diagonal must be
			// 0. This is the driver-independent acceptance guard for the
			// `nvidia-smi topo -m` NV# matrix (the e2e is the integration
			// reveal; this is the deterministic oracle). A partially
			// populated matrix or a wrong count (e.g. NV1) fails here.
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					want := c.wantNV
					if i == j {
						want = 0
					}
					got := f.NVLinkCount(i, j)
					require.Equal(t, want, got, "%s: NVLinkCount(%d,%d) = %d, want %d", c.profile, i, j, got, want)
				}
			}
		})
	}
}

// TestNodeFabric_ConcurrentReads hammers the read-only fabric accessors
// from many goroutines; with -race this guards the immutability claim.
func TestNodeFabric_ConcurrentReads(t *testing.T) {
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.0", NumDevices: 2},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
			devWithBDF(1, "0000:0B:00.0"),
		},
		NVLink: &NVLinkConfig{
			Version:              5,
			BandwidthPerLinkGBPS: 100,
			Switches:             []NVSwitchConfig{{BDF: "0000:0F:00.0"}},
			Defaults:             &NVLinkDefaults{State: "active", DutyCycle: 0.05},
			DeviceLinks: []DeviceLinksConfig{
				{Index: 0, Links: switchLinks(18, "0000:0F:00.0")},
				{Index: 1, Links: switchLinks(18, "0000:0F:00.0")},
			},
		},
		PCIeTopology: &PCIeTopologyConfig{
			CoresPerNUMA: 8,
			RootComplexes: []RootComplexConfig{
				{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:0A:00.0", "0000:0B:00.0"}},
			},
		},
	}
	f := BuildNodeFabric(&Config{NumDevices: 2, YAMLConfig: yc})

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_ = f.NVLinkCount(0, 1)
				_ = f.TopoLevel(0, 1)
				_, _ = f.Link(0, i%18)
				_ = f.CPUAffinityMask(0, 4)
				_, _ = f.NvLinkCounters(0, 0, f.now())
			}
		}()
	}
	wg.Wait()
}
