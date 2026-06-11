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
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	"github.com/stretchr/testify/require"
)

func makeFabricDevice(t *testing.T, fabric *FabricConfig) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*dgxa100.Device)
	return NewConfigurableDevice(0, bd, &DeviceConfig{Fabric: fabric},
		"GPU-00000000-0000-0000-0000-000000000000", "0000:01:00.0", 0, nil)
}

func TestGetMockFabricInfo_NotSupportedWhenNil(t *testing.T) {
	dev := makeFabricDevice(t, nil)
	_, ret := dev.GetMockFabricInfo()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "nil fabric")
	_, ret = dev.GetMockFabricInfoV()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "nil fabric V")
}

func TestGetMockFabricInfo_PopulatesFields(t *testing.T) {
	dev := makeFabricDevice(t, &FabricConfig{
		ClusterUUID: "00000000-0000-0000-0000-0000000000ab",
		CliqueID:    7,
		State:       "completed",
		HealthMask:  0x42,
	})
	info, ret := dev.GetMockFabricInfo()
	require.Equal(t, nvml.SUCCESS, ret, "v1")
	require.Equal(t, uint32(7), info.CliqueID, "CliqueID")
	require.Equal(t, FabricStateCompleted, info.State, "State")
	require.Equal(t, byte(0xab), info.ClusterUUID[15], "ClusterUUID last byte")

	v, ret := dev.GetMockFabricInfoV()
	require.Equal(t, nvml.SUCCESS, ret, "V")
	require.Equal(t, uint32(0x42), v.HealthMask, "V healthMask")
}

func TestParseFabricState(t *testing.T) {
	cases := map[string]uint8{
		"":             FabricStateCompleted,
		"completed":    FabricStateCompleted,
		"not_started":  FabricStateNotStarted,
		"in_progress":  FabricStateInProgress,
		"InProgress":   FabricStateInProgress,
		"NotSupported": FabricStateNotSupported,
		"garbage":      FabricStateCompleted,
	}
	for in, want := range cases {
		require.Equal(t, want, parseFabricState(in), "parseFabricState(%q)", in)
	}
}

func TestParseClusterUUID(t *testing.T) {
	out := parseClusterUUID("00000000-0000-0000-0000-000000000001")
	require.Equal(t, byte(0x01), out[15], "last byte")
	// short input is zero-padded
	short := parseClusterUUID("ff")
	require.Equal(t, byte(0xff), short[0], "short first byte")
	require.Equal(t, byte(0x00), short[15], "short last byte")
}

func TestTopologyOverlay(t *testing.T) {
	dir := t.TempDir()
	topoPath := filepath.Join(dir, "topology.yaml")
	err := os.WriteFile(topoPath, []byte(`
version: 1
domains:
  - name: dom1
    uuid: "00000000-0000-0000-0000-0000000000ab"
    cliques:
      - id: 5
        nodes: [nodeA, nodeB]
      - id: 9
        nodes: [nodeC]
`), 0o644)
	require.NoError(t, err)
	t.Setenv("NODE_NAME", "nodeB")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", topoPath)

	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{
			ClusterUUID: "ZERO",
			CliqueID:    0,
		}},
	}
	applyTopologyOverlay(cfg)
	require.Equal(t, uint32(5), cfg.DeviceDefaults.Fabric.CliqueID, "CliqueID")
	require.Equal(t, "00000000-0000-0000-0000-0000000000ab", cfg.DeviceDefaults.Fabric.ClusterUUID, "ClusterUUID")
}

func TestTopologyOverlay_NoFabricDefaults_NoOp(t *testing.T) {
	// Regression: with topology.enabled=true but a non-fabric profile
	// (e.g. a100) DeviceDefaults.Fabric stays nil so the overlay must
	// not synthesise a FabricConfig — otherwise every A100 would start
	// reporting GB200-style fabric info.
	dir := t.TempDir()
	topoPath := filepath.Join(dir, "topology.yaml")
	_ = os.WriteFile(topoPath, []byte(`
version: 1
domains:
  - uuid: "00000000-0000-0000-0000-0000000000ab"
    cliques:
      - id: 5
        nodes: [nodeA]
`), 0o644)
	t.Setenv("NODE_NAME", "nodeA")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", topoPath)

	cfg := &YAMLConfig{DeviceDefaults: DeviceConfig{}}
	applyTopologyOverlay(cfg)
	require.Nil(t, cfg.DeviceDefaults.Fabric, "non-fabric profile: want Fabric=nil after overlay")
}

func TestTopologyOverlay_NodeNotPresent_NoChange(t *testing.T) {
	dir := t.TempDir()
	topoPath := filepath.Join(dir, "topology.yaml")
	_ = os.WriteFile(topoPath, []byte(`
version: 1
domains:
  - uuid: aaaa
    cliques:
      - id: 0
        nodes: [otherNode]
`), 0o644)
	t.Setenv("NODE_NAME", "missingNode")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", topoPath)

	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{
			ClusterUUID: "ORIG",
			CliqueID:    77,
		}},
	}
	applyTopologyOverlay(cfg)
	require.Equal(t, uint32(77), cfg.DeviceDefaults.Fabric.CliqueID, "unexpected mutation")
	require.Equal(t, "ORIG", cfg.DeviceDefaults.Fabric.ClusterUUID, "unexpected mutation")
}

// TestTopologyOverlay_MissingFile_NoChange exercises the os.Stat
// early-return branch in applyTopologyOverlay: when MOCK_TOPOLOGY_CONFIG
// points at a path that does not exist, the overlay must be a silent
// no-op, leaving DeviceDefaults.Fabric exactly as the YAML profile set it.
func TestTopologyOverlay_MissingFile_NoChange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NODE_NAME", "nodeA")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", filepath.Join(dir, "does-not-exist.yaml"))

	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{
			ClusterUUID: "ORIG-UUID",
			CliqueID:    42,
		}},
	}
	applyTopologyOverlay(cfg)
	require.NotNil(t, cfg.DeviceDefaults.Fabric, "Fabric reset to nil; want sentinel values preserved")
	require.Equal(t, "ORIG-UUID", cfg.DeviceDefaults.Fabric.ClusterUUID, "Fabric mutated by missing-file path")
	require.Equal(t, uint32(42), cfg.DeviceDefaults.Fabric.CliqueID, "Fabric mutated by missing-file path")
}

// TestTopologyOverlay_MalformedYAML_NoChange exercises the yaml.Unmarshal
// failure branch. A typo in the topology file should warn and skip the
// overlay rather than crash or partially apply.
func TestTopologyOverlay_MalformedYAML_NoChange(t *testing.T) {
	dir := t.TempDir()
	topoPath := filepath.Join(dir, "topology.yaml")
	require.NoError(t, os.WriteFile(topoPath, []byte("this: is: not: valid: yaml\n  - mismatched indent\n"), 0o644))
	t.Setenv("NODE_NAME", "nodeA")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", topoPath)

	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{
			ClusterUUID: "ORIG-UUID",
			CliqueID:    42,
		}},
	}
	applyTopologyOverlay(cfg)
	require.NotNil(t, cfg.DeviceDefaults.Fabric, "Fabric reset to nil; want sentinel values preserved")
	require.Equal(t, "ORIG-UUID", cfg.DeviceDefaults.Fabric.ClusterUUID, "Fabric mutated by malformed-YAML path")
	require.Equal(t, uint32(42), cfg.DeviceDefaults.Fabric.CliqueID, "Fabric mutated by malformed-YAML path")
}

// TestTopologyOverlay_UnreadableFile_NoChange exercises the os.ReadFile
// failure branch by pointing MOCK_TOPOLOGY_CONFIG at a directory. The
// path exists (so os.Stat succeeds), but the subsequent ReadFile fails
// with "is a directory" and the overlay must skip cleanly.
func TestTopologyOverlay_UnreadableFile_NoChange(t *testing.T) {
	dir := t.TempDir() // directory, not a file — defeats ReadFile
	t.Setenv("NODE_NAME", "nodeA")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", dir)

	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{
			ClusterUUID: "ORIG-UUID",
			CliqueID:    42,
		}},
	}
	applyTopologyOverlay(cfg)
	require.NotNil(t, cfg.DeviceDefaults.Fabric, "Fabric reset to nil; want sentinel values preserved")
	require.Equal(t, "ORIG-UUID", cfg.DeviceDefaults.Fabric.ClusterUUID, "Fabric mutated by unreadable-file path")
	require.Equal(t, uint32(42), cfg.DeviceDefaults.Fabric.CliqueID, "Fabric mutated by unreadable-file path")
}
