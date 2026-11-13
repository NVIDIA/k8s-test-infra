// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

const (
	unicastPointer    = uint8(nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_POINTER)
	multicastPointer  = uint8(nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_POINTER)
	unicastEndpoint   = uint8(nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_LOGICAL_ENDPOINT)
	multicastEndpoint = uint8(nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_LOGICAL_ENDPOINT)
)

func TestGetMockFabricInfoV4_NotSupportedWithoutFabric(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "blackwell"})

	_, ret := dev.GetMockFabricInfoV4()

	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// The driver reports more clique types with each generation; every one of them
// follows clique_id unless the profile says otherwise.
func TestGetMockFabricInfoV4_CliqueTypesFollowArchitecture(t *testing.T) {
	t.Parallel()
	cases := map[string][]FabricClique{
		"ampere":    {{unicastPointer, 7}},
		"hopper":    {{unicastPointer, 7}, {multicastPointer, 7}},
		"blackwell": {{unicastPointer, 7}, {multicastPointer, 7}, {unicastEndpoint, 7}, {multicastEndpoint, 7}},
		"rubin":     {{unicastPointer, 7}, {multicastPointer, 7}, {unicastEndpoint, 7}, {multicastEndpoint, 7}},
	}
	for arch, want := range cases {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, &DeviceConfig{
				Architecture: arch,
				Fabric:       &FabricConfig{CliqueID: 7},
			})

			info, ret := dev.GetMockFabricInfoV4()

			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, want, info.Cliques)
		})
	}
}

// A consumer moving from v3 to v4 must read the same fabric: the header maps
// the legacy cliqueId onto the first unicast-pointer entry.
func TestGetMockFabricInfoV4_AgreesWithV3(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "blackwell",
		Fabric: &FabricConfig{
			ClusterUUID: "00000000-0000-0000-0000-0000000000ab",
			CliqueID:    7,
			State:       "in_progress",
			Health:      &FabricHealthConfig{RouteUnhealthy: true},
			Cliques:     []FabricCliqueConfig{{Type: "multicast_pointer", ID: 3}},
		},
	})

	v3, ret := dev.GetMockFabricInfoV()
	require.Equal(t, nvml.SUCCESS, ret)
	v4, ret := dev.GetMockFabricInfoV4()
	require.Equal(t, nvml.SUCCESS, ret)

	require.Equal(t, FabricClique{Type: unicastPointer, ID: v3.CliqueID}, v4.Cliques[0])
	v4.Cliques = nil
	require.Equal(t, v3, v4)
}

func TestGetMockFabricInfoV4_ProfileCliquesReplaceTheirType(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		arch    string
		cliques []FabricCliqueConfig
		want    []FabricClique
	}{
		// What a Vera Rubin NVL72 tray reports.
		"endpoint cliques differ from the pointer clique": {
			arch: "rubin",
			cliques: []FabricCliqueConfig{
				{Type: "unicast_logical_endpoint", ID: 32766},
				{Type: "multicast_logical_endpoint", ID: 32766},
			},
			want: []FabricClique{
				{unicastPointer, 65534}, {multicastPointer, 65534},
				{unicastEndpoint, 32766}, {multicastEndpoint, 32766},
			},
		},
		"several cliques of one type are sorted by id": {
			arch: "hopper",
			cliques: []FabricCliqueConfig{
				{Type: "multicast_pointer", ID: 9},
				{Type: "multicast_pointer", ID: 2},
				{Type: "multicast_pointer", ID: 9},
			},
			want: []FabricClique{{unicastPointer, 65534}, {multicastPointer, 2}, {multicastPointer, 9}},
		},
		"a type the architecture does not report by default": {
			arch:    "hopper",
			cliques: []FabricCliqueConfig{{Type: "Unicast_Logical_Endpoint", ID: 4}},
			want:    []FabricClique{{unicastPointer, 65534}, {multicastPointer, 65534}, {unicastEndpoint, 4}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, &DeviceConfig{
				Architecture: tc.arch,
				Fabric:       &FabricConfig{CliqueID: 65534, Cliques: tc.cliques},
			})

			info, ret := dev.GetMockFabricInfoV4()

			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tc.want, info.Cliques)
		})
	}
}

// Pins the shipped profile to the <cliques> block of the tray it was captured
// from (tests/hardware-captures/vr-nvl72), whose logical-endpoint cliques do
// not carry the pointer cliques' id.
func TestGetMockFabricInfoV4_VeraRubinProfileReportsTheCapturedCliques(t *testing.T) {
	t.Parallel()
	dev := shippedProfileDevice(t, "vrnvl72", "")

	info, ret := dev.GetMockFabricInfoV4()

	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []FabricClique{
		{unicastPointer, 65534}, {multicastPointer, 65534},
		{unicastEndpoint, 32766}, {multicastEndpoint, 32766},
	}, info.Cliques)
}

func TestValidateConfig_FabricCliques(t *testing.T) {
	t.Parallel()
	tooMany := make([]FabricCliqueConfig, nvml.GPU_FABRIC_CLIQUE_MAX)
	for i := range tooMany {
		tooMany[i] = FabricCliqueConfig{Type: "multicast_pointer", ID: uint32(i)}
	}
	cases := map[string]struct {
		cliques []FabricCliqueConfig
		wantErr string
	}{
		"every overridable type": {
			cliques: []FabricCliqueConfig{
				{Type: "multicast_pointer", ID: 1},
				{Type: "unicast_logical_endpoint", ID: 2},
				{Type: "multicast_logical_endpoint", ID: 3},
			},
		},
		"unknown type": {
			cliques: []FabricCliqueConfig{{Type: "broadcast", ID: 1}},
			wantErr: `fabric.cliques[0].type "broadcast"`,
		},
		// clique_id is the unicast-pointer clique; a second source for it
		// could make v3 and v4 disagree.
		"unicast pointer": {
			cliques: []FabricCliqueConfig{{Type: "unicast_pointer", ID: 1}},
			wantErr: "clique_id",
		},
		"more than the struct holds": {
			cliques: tooMany,
			wantErr: "more than the 64",
		},
		// The two logical-endpoint types are not listed, so a Blackwell board
		// adds their default cliques and the struct would overflow by two.
		"no room left for the types it leaves out": {
			cliques: tooMany[:nvml.GPU_FABRIC_CLIQUE_MAX-1],
			wantErr: "more than the 64",
		},
		"exactly fills the struct": {
			cliques: tooMany[:nvml.GPU_FABRIC_CLIQUE_MAX-3],
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defaults := validFabricConfig(&FabricConfig{Cliques: tc.cliques})
			perDevice := validFabricConfig(nil)
			perDevice.Devices = []DeviceOverride{{Index: 0, DeviceConfig: DeviceConfig{Fabric: &FabricConfig{Cliques: tc.cliques}}}}

			for scope, config := range map[string]*YAMLConfig{"device_defaults": defaults, "devices[0]": perDevice} {
				err := ValidateConfig(config)
				if tc.wantErr == "" {
					require.NoError(t, err, scope)
					continue
				}
				require.ErrorContains(t, err, scope)
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

func validFabricConfig(fabric *FabricConfig) *YAMLConfig {
	return &YAMLConfig{
		Version:        "1.0",
		System:         SystemConfig{DriverVersion: "580.65.06"},
		DeviceDefaults: DeviceConfig{Fabric: fabric},
	}
}

// The topology places a node in one clique, so the profile's per-type ids,
// which describe a node outside any topology, must not survive next to it.
func TestTopologyOverlay_DropsProfileCliques(t *testing.T) {
	topoPath := filepath.Join(t.TempDir(), "topology.yaml")
	require.NoError(t, os.WriteFile(topoPath, []byte(`
domains:
  - uuid: "00000000-0000-0000-0000-0000000000ab"
    cliques:
      - id: 5
        nodes: [nodeA]
`), 0o644))
	t.Setenv("NODE_NAME", "nodeA")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", topoPath)

	profileCliques := []FabricCliqueConfig{{Type: "unicast_logical_endpoint", ID: 32766}}
	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{CliqueID: 65534, Cliques: profileCliques}},
		Devices:        []DeviceOverride{{DeviceConfig: DeviceConfig{Fabric: &FabricConfig{Cliques: profileCliques}}}},
	}
	applyTopologyOverlay(cfg, "")

	require.Empty(t, cfg.DeviceDefaults.Fabric.Cliques)
	require.Empty(t, cfg.Devices[0].Fabric.Cliques)
}
