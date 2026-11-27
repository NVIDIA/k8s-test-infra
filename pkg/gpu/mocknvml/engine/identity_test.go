// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"fmt"
	"os"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func loadProfile(t *testing.T, name string) *YAMLConfig {
	t.Helper()
	data, err := os.ReadFile("../../../../deployments/nvml-mock/helm/nvml-mock/profiles/" + name + ".yaml")
	require.NoError(t, err)
	var cfg YAMLConfig
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	return &cfg
}

func TestApplyNodeIdentity_NodesDiffer(t *testing.T) {
	t.Parallel()

	a, b := loadProfile(t, "h100"), loadProfile(t, "h100")
	require.NoError(t, ApplyNodeIdentity(a, "worker-1", nil))
	require.NoError(t, ApplyNodeIdentity(b, "worker-2", nil))

	seen := map[string]string{}
	for node, cfg := range map[string]*YAMLConfig{"worker-1": a, "worker-2": b} {
		for _, dev := range cfg.Devices {
			require.Regexp(t, `^GPU-[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`, dev.UUID)
			owner, dup := seen[dev.UUID]
			require.False(t, dup, "%s and %s report %s", owner, node, dev.UUID)
			seen[dev.UUID] = node
		}
	}
	require.NotEqual(t, a.Devices[0].Serial, b.Devices[0].Serial)
}

func TestApplyNodeIdentity_Deterministic(t *testing.T) {
	t.Parallel()

	a, b := loadProfile(t, "gb200"), loadProfile(t, "gb200")
	require.NoError(t, ApplyNodeIdentity(a, "worker-7", nil))
	require.NoError(t, ApplyNodeIdentity(b, "worker-7", nil))
	require.Equal(t, a, b)
}

func TestApplyNodeIdentity_KeepsProfileStructure(t *testing.T) {
	t.Parallel()

	orig, cfg := loadProfile(t, "gb200"), loadProfile(t, "gb200")
	require.NoError(t, ApplyNodeIdentity(cfg, "worker-1", nil))

	// GPUs 0 and 1 are one superchip board in the profile, and still are.
	require.Equal(t, orig.Devices[0].Serial, orig.Devices[1].Serial)
	require.Equal(t, cfg.Devices[0].Serial, cfg.Devices[1].Serial)
	require.NotEqual(t, cfg.Devices[0].Serial, cfg.Devices[2].Serial)
	require.NotEqual(t, cfg.Devices[0].UUID, cfg.Devices[1].UUID)
	require.Len(t, cfg.Devices[0].Serial, len(orig.Devices[0].Serial))
	// Without a topology the node's rack position is not known.
	require.Equal(t, orig.DeviceDefaults.Platform, cfg.DeviceDefaults.Platform)
}

func TestApplyNodeIdentity_NoNodeName(t *testing.T) {
	t.Parallel()
	require.Error(t, ApplyNodeIdentity(loadProfile(t, "h100"), "", nil))
}

func TestApplyNodeIdentity_RackPosition(t *testing.T) {
	t.Parallel()

	nodes := make([]string, 18)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("n%d", i+1)
	}
	topo := &TopologyDocument{Domains: []TopologyDomain{{
		Name: "rack-1", UUID: "00000000-0000-0000-0000-000000000001",
		Cliques: []TopologyClique{{ID: 0, Nodes: nodes[:10]}, {ID: 1, Nodes: nodes[10:]}},
	}}}

	chassis := map[string]bool{}
	slots := map[uint8]bool{}
	for i, node := range nodes {
		cfg := loadProfile(t, "gb200")
		require.NoError(t, ApplyNodeIdentity(cfg, node, topo))
		p := cfg.DeviceDefaults.Platform
		require.EqualValues(t, i+1, p.TrayIndex, node)
		slots[p.SlotNumber] = true
		chassis[p.ChassisSerialNumber] = true
	}
	require.Len(t, chassis, 1, "one rack, one chassis")
	require.Len(t, slots, 18, "no two trays share a slot")

	// The gb200 profile was captured at tray 11, slot 21: that node reports
	// exactly that, and the others keep the profile's offset from it.
	cfg := loadProfile(t, "gb200")
	require.NoError(t, ApplyNodeIdentity(cfg, "n11", topo))
	require.EqualValues(t, 11, cfg.DeviceDefaults.Platform.TrayIndex)
	require.EqualValues(t, 21, cfg.DeviceDefaults.Platform.SlotNumber)
}

func TestApplyNodeIdentity_RackPositionKeepsCapturedOffset(t *testing.T) {
	t.Parallel()

	// The vrnvl72 profile is a real capture: tray 3 sits in slot 4.
	topo := &TopologyDocument{Domains: []TopologyDomain{{
		UUID: "d", Cliques: []TopologyClique{{Nodes: []string{"a", "b", "c"}}},
	}}}
	cfg := loadProfile(t, "vrnvl72")
	require.NoError(t, ApplyNodeIdentity(cfg, "c", topo))
	require.EqualValues(t, 3, cfg.DeviceDefaults.Platform.TrayIndex)
	require.EqualValues(t, 4, cfg.DeviceDefaults.Platform.SlotNumber)

	cfg = loadProfile(t, "vrnvl72")
	require.NoError(t, ApplyNodeIdentity(cfg, "a", topo))
	require.EqualValues(t, 1, cfg.DeviceDefaults.Platform.TrayIndex)
	require.EqualValues(t, 2, cfg.DeviceDefaults.Platform.SlotNumber)
}

func TestApplyNodeIdentity_NodeOutsideTopologyKeepsPosition(t *testing.T) {
	t.Parallel()

	orig, cfg := loadProfile(t, "gb200"), loadProfile(t, "gb200")
	topo := &TopologyDocument{Domains: []TopologyDomain{{UUID: "d", Cliques: []TopologyClique{{Nodes: []string{"other"}}}}}}
	require.NoError(t, ApplyNodeIdentity(cfg, "worker-1", topo))
	require.Equal(t, orig.DeviceDefaults.Platform, cfg.DeviceDefaults.Platform)
}

func TestApplyNodeIdentity_ProfileStillValid(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"a100", "h100", "b200", "gb200", "gb300", "l40s", "t4", "vrnvl72"} {
		cfg := loadProfile(t, name)
		require.NoError(t, ApplyNodeIdentity(cfg, "worker-1", nil), name)
		require.NoError(t, ValidateConfig(cfg), name)
	}
}

func TestDerivedSerial_ShortSerialKeepsLength(t *testing.T) {
	t.Parallel()
	require.Len(t, derivedSerial("n", "12345"), 5)
	require.Regexp(t, `^1234[0-9]{9}$`, derivedSerial("n", "1234567890123"))
}

func TestApplyNodeIdentity_AppliesOnce(t *testing.T) {
	t.Parallel()

	cfg := loadProfile(t, "h100")
	require.NoError(t, ApplyNodeIdentity(cfg, "worker-1", nil))
	derived := cfg.Devices[0].UUID

	// The agent stages a derived config, which the engine may load again.
	require.NoError(t, ApplyNodeIdentity(cfg, "worker-1", nil))
	require.Equal(t, derived, cfg.Devices[0].UUID)

	require.Error(t, ApplyNodeIdentity(cfg, "worker-2", nil))
}

func TestApplyIdentityOverlay(t *testing.T) { //nolint:paralleltest // sets process environment
	path := "../../../../deployments/nvml-mock/helm/nvml-mock/profiles/h100.yaml"
	load := func() *YAMLConfig {
		cfg := loadProfile(t, "h100")
		applyIdentityOverlay(cfg, path)
		return cfg
	}
	orig := loadProfile(t, "h100")

	t.Setenv("NODE_NAME", "worker-1")
	require.Equal(t, orig.Devices[0].UUID, load().Devices[0].UUID, "off by default")

	t.Setenv(EnvIdentityDerive, IdentityDeriveNode)
	one := load().Devices[0].UUID
	require.NotEqual(t, orig.Devices[0].UUID, one)

	t.Setenv("NODE_NAME", "worker-2")
	require.NotEqual(t, one, load().Devices[0].UUID)

	t.Setenv("NODE_NAME", "")
	require.Equal(t, orig.Devices[0].UUID, load().Devices[0].UUID, "no node name keeps the profile's")

	t.Setenv(EnvIdentityDerive, "random")
	t.Setenv("NODE_NAME", "worker-1")
	require.Equal(t, orig.Devices[0].UUID, load().Devices[0].UUID, "unknown mode keeps the profile's")
}

// TestIdentityDerive_ReportedThroughNVML follows the derivation to what a
// consumer reads: nvmlDeviceGetUUID and nvmlDeviceGetSerial, on two nodes
// loading one profile.
func TestIdentityDerive_ReportedThroughNVML(t *testing.T) { //nolint:paralleltest // sets process environment
	profile := "../../../../deployments/nvml-mock/helm/nvml-mock/profiles/h100.yaml"
	t.Setenv("MOCK_NVML_CONFIG", profile)
	t.Setenv(EnvIdentityDerive, IdentityDeriveNode)

	report := func(node string) (uuids, serials []string) {
		t.Setenv("NODE_NAME", node)
		ClearConfigCache()
		t.Cleanup(ClearConfigCache)
		e := NewEngine(nil)
		require.Equal(t, nvml.SUCCESS, e.Init())
		defer func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) }()
		count, ret := e.DeviceGetCount()
		require.Equal(t, nvml.SUCCESS, ret)
		for i := range count {
			dev := e.server.configurableDevices[i]
			uuid, ret := dev.GetUUID()
			require.Equal(t, nvml.SUCCESS, ret)
			serial, ret := dev.GetSerial()
			require.Equal(t, nvml.SUCCESS, ret)
			uuids, serials = append(uuids, uuid), append(serials, serial)
		}
		return uuids, serials
	}

	u1, s1 := report("worker-1")
	u2, s2 := report("worker-2")
	require.NotEmpty(t, u1)
	require.Len(t, u2, len(u1))
	for i := range u1 {
		require.NotEqual(t, u1[i], u2[i])
		require.NotEqual(t, s1[i], s2[i])
	}
	again, _ := report("worker-1")
	require.Equal(t, u1, again, "the same node always reports the same GPUs")
}
