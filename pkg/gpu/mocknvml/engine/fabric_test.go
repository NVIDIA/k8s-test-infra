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
)

func makeFabricDevice(t *testing.T, fabric *FabricConfig) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*dgxa100.Device)
	return NewConfigurableDevice(0, bd, &DeviceConfig{Fabric: fabric},
		"GPU-00000000-0000-0000-0000-000000000000", "00000000:01:00.0", 0, nil)
}

func TestGetMockFabricInfo_NotSupportedWhenNil(t *testing.T) {
	dev := makeFabricDevice(t, nil)
	_, ret := dev.GetMockFabricInfo()
	if ret != nvml.ERROR_NOT_SUPPORTED {
		t.Fatalf("nil fabric: want ERROR_NOT_SUPPORTED, got %v", ret)
	}
	_, ret = dev.GetMockFabricInfoV()
	if ret != nvml.ERROR_NOT_SUPPORTED {
		t.Fatalf("nil fabric V: want ERROR_NOT_SUPPORTED, got %v", ret)
	}
}

func TestGetMockFabricInfo_PopulatesFields(t *testing.T) {
	dev := makeFabricDevice(t, &FabricConfig{
		ClusterUUID: "00000000-0000-0000-0000-0000000000ab",
		CliqueID:    7,
		State:       "completed",
		HealthMask:  0x42,
	})
	info, ret := dev.GetMockFabricInfo()
	if ret != nvml.SUCCESS {
		t.Fatalf("v1: %v", ret)
	}
	if info.CliqueID != 7 {
		t.Errorf("CliqueID: want 7, got %d", info.CliqueID)
	}
	if info.State != FabricStateCompleted {
		t.Errorf("State: want completed (%d), got %d", FabricStateCompleted, info.State)
	}
	if info.ClusterUUID[15] != 0xab {
		t.Errorf("ClusterUUID last byte: want 0xab, got 0x%x", info.ClusterUUID[15])
	}

	v, ret := dev.GetMockFabricInfoV()
	if ret != nvml.SUCCESS {
		t.Fatalf("V: %v", ret)
	}
	if v.HealthMask != 0x42 {
		t.Errorf("V: healthMask=%d", v.HealthMask)
	}
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
		if got := parseFabricState(in); got != want {
			t.Errorf("parseFabricState(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseClusterUUID(t *testing.T) {
	out := parseClusterUUID("00000000-0000-0000-0000-000000000001")
	if out[15] != 0x01 {
		t.Errorf("last byte: want 0x01, got 0x%x", out[15])
	}
	// short input is zero-padded
	short := parseClusterUUID("ff")
	if short[0] != 0xff || short[15] != 0x00 {
		t.Errorf("short: want [ff..00], got %x", short)
	}
}

func TestTopologyOverlay(t *testing.T) {
	dir := t.TempDir()
	topoPath := filepath.Join(dir, "topology.yaml")
	if err := os.WriteFile(topoPath, []byte(`
version: 1
domains:
  - name: dom1
    uuid: "00000000-0000-0000-0000-0000000000ab"
    cliques:
      - id: 5
        nodes: [nodeA, nodeB]
      - id: 9
        nodes: [nodeC]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_NAME", "nodeB")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", topoPath)

	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{
			ClusterUUID: "ZERO",
			CliqueID:    0,
		}},
	}
	applyTopologyOverlay(cfg)
	if cfg.DeviceDefaults.Fabric.CliqueID != 5 {
		t.Errorf("CliqueID: want 5, got %d", cfg.DeviceDefaults.Fabric.CliqueID)
	}
	if cfg.DeviceDefaults.Fabric.ClusterUUID != "00000000-0000-0000-0000-0000000000ab" {
		t.Errorf("ClusterUUID: got %q", cfg.DeviceDefaults.Fabric.ClusterUUID)
	}
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
	if cfg.DeviceDefaults.Fabric != nil {
		t.Errorf("non-fabric profile: want Fabric=nil after overlay, got %+v", cfg.DeviceDefaults.Fabric)
	}
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
	if cfg.DeviceDefaults.Fabric.CliqueID != 77 || cfg.DeviceDefaults.Fabric.ClusterUUID != "ORIG" {
		t.Errorf("unexpected mutation: %+v", cfg.DeviceDefaults.Fabric)
	}
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
	if cfg.DeviceDefaults.Fabric == nil {
		t.Fatal("Fabric reset to nil; want sentinel values preserved")
	}
	if cfg.DeviceDefaults.Fabric.ClusterUUID != "ORIG-UUID" || cfg.DeviceDefaults.Fabric.CliqueID != 42 {
		t.Errorf("Fabric mutated by missing-file path: %+v", cfg.DeviceDefaults.Fabric)
	}
}

// TestTopologyOverlay_MalformedYAML_NoChange exercises the yaml.Unmarshal
// failure branch. A typo in the topology file should warn and skip the
// overlay rather than crash or partially apply.
func TestTopologyOverlay_MalformedYAML_NoChange(t *testing.T) {
	dir := t.TempDir()
	topoPath := filepath.Join(dir, "topology.yaml")
	if err := os.WriteFile(topoPath, []byte("this: is: not: valid: yaml\n  - mismatched indent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_NAME", "nodeA")
	t.Setenv("MOCK_TOPOLOGY_CONFIG", topoPath)

	cfg := &YAMLConfig{
		DeviceDefaults: DeviceConfig{Fabric: &FabricConfig{
			ClusterUUID: "ORIG-UUID",
			CliqueID:    42,
		}},
	}
	applyTopologyOverlay(cfg)
	if cfg.DeviceDefaults.Fabric == nil {
		t.Fatal("Fabric reset to nil; want sentinel values preserved")
	}
	if cfg.DeviceDefaults.Fabric.ClusterUUID != "ORIG-UUID" || cfg.DeviceDefaults.Fabric.CliqueID != 42 {
		t.Errorf("Fabric mutated by malformed-YAML path: %+v", cfg.DeviceDefaults.Fabric)
	}
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
	if cfg.DeviceDefaults.Fabric == nil {
		t.Fatal("Fabric reset to nil; want sentinel values preserved")
	}
	if cfg.DeviceDefaults.Fabric.ClusterUUID != "ORIG-UUID" || cfg.DeviceDefaults.Fabric.CliqueID != 42 {
		t.Errorf("Fabric mutated by unreadable-file path: %+v", cfg.DeviceDefaults.Fabric)
	}
}
