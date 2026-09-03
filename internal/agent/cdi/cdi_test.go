// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cdi

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

func testState() *agent.State {
	return &agent.State{
		Software: agent.SoftwareVersions{DriverVersion: "550.163.01"},
		Devices: []agent.DeviceSpec{
			{Index: 0, UUID: "GPU-abc123", MinorNumber: 0},
			{Index: 1, UUID: "GPU-def456", MinorNumber: 1},
		},
	}
}

func TestApplyWritesSpecs(t *testing.T) {
	h := host.New(t.TempDir())
	state := testState()
	s := New(h)
	ctx := context.Background()

	require.NoError(t, s.Stage(ctx, state))
	require.True(t, s.Ready())
	require.NoError(t, s.Apply(ctx, state))

	require.FileExists(t, h.RunPath(nvidiaSpecFile))
	require.FileExists(t, h.RunPath(nriSpecFile))
}

func TestNvidiaSpec(t *testing.T) {
	h := host.New(t.TempDir())
	state := testState()
	s := New(h)
	ctx := context.Background()
	require.NoError(t, s.Stage(ctx, state))
	require.NoError(t, s.Apply(ctx, state))

	data, err := os.ReadFile(h.RunPath(nvidiaSpecFile))
	require.NoError(t, err)

	var spec cdiSpec
	require.NoError(t, yaml.Unmarshal(data, &spec))

	require.Equal(t, "0.6.0", spec.CDIVersion)
	require.Equal(t, "nvidia.com/gpu", spec.Kind)
	require.NotNil(t, spec.ContainerEdits)

	// 2 GPUs × (index + UUID) + "all" = 5 devices
	require.Len(t, spec.Devices, 5)
	require.Equal(t, "0", spec.Devices[0].Name)
	require.Equal(t, "GPU-abc123", spec.Devices[1].Name)
	require.Equal(t, "1", spec.Devices[2].Name)
	require.Equal(t, "GPU-def456", spec.Devices[3].Name)
	require.Equal(t, "all", spec.Devices[4].Name)

	// "all" has 2 per-GPU device nodes
	require.Len(t, spec.Devices[4].ContainerEdits.DeviceNodes, 2)

	// Shared device nodes must include control devices
	paths := make([]string, 0, len(spec.ContainerEdits.DeviceNodes))
	for _, dn := range spec.ContainerEdits.DeviceNodes {
		paths = append(paths, dn.Path)
	}
	require.Contains(t, paths, "/dev/nvidiactl")
	require.Contains(t, paths, "/dev/nvidia-uvm")
	require.Contains(t, paths, "/dev/nvidia-uvm-tools")
}

func TestNRISpec(t *testing.T) {
	h := host.New(t.TempDir())
	state := testState()
	s := New(h)
	ctx := context.Background()
	require.NoError(t, s.Stage(ctx, state))
	require.NoError(t, s.Apply(ctx, state))

	data, err := os.ReadFile(h.RunPath(nriSpecFile))
	require.NoError(t, err)

	var spec cdiSpec
	require.NoError(t, yaml.Unmarshal(data, &spec))

	require.Equal(t, "0.6.0", spec.CDIVersion)
	require.Equal(t, "nvml-mock.nvidia.com/gpu", spec.Kind)
	require.NotNil(t, spec.ContainerEdits)
	require.Contains(t, spec.ContainerEdits.Env, "NVML_MOCK_DEVICE_SOURCE=cdi")

	// No library mounts or hooks — the NRI overlay bind-mount delivers those.
	require.Empty(t, spec.ContainerEdits.Mounts)
	require.Empty(t, spec.ContainerEdits.Hooks)

	// 2 GPUs (index only, no UUID) + "all" = 3 devices
	require.Len(t, spec.Devices, 3)
	require.Equal(t, "0", spec.Devices[0].Name)
	require.Equal(t, "1", spec.Devices[1].Name)
	require.Equal(t, "all", spec.Devices[2].Name)

	// "all" has 2 per-GPU + 3 control nodes = 5
	require.Len(t, spec.Devices[2].ContainerEdits.DeviceNodes, 5)
}

func TestFabricStateMount(t *testing.T) {
	h := host.New(t.TempDir())
	state := testState()
	state.Fabric.ManagerStateDir = "/var/lib/nvml-mock/fabric-state"
	s := New(h)
	ctx := context.Background()
	require.NoError(t, s.Stage(ctx, state))
	require.NoError(t, s.Apply(ctx, state))

	data, err := os.ReadFile(h.RunPath(nvidiaSpecFile))
	require.NoError(t, err)
	var spec cdiSpec
	require.NoError(t, yaml.Unmarshal(data, &spec))

	var hasFabricMount bool
	for _, m := range spec.ContainerEdits.Mounts {
		if m.HostPath == "/var/lib/nvml-mock/fabric-state" {
			hasFabricMount = true
		}
	}
	require.True(t, hasFabricMount, "expected fabric-state mount in nvidia.yaml")

	var hasFabricEnv bool
	for _, e := range spec.ContainerEdits.Env {
		if e == "MOCK_FABRICMANAGER_STATE_DIR=/var/lib/nvml-mock/fabric-state" {
			hasFabricEnv = true
		}
	}
	require.True(t, hasFabricEnv, "expected MOCK_FABRICMANAGER_STATE_DIR in nvidia.yaml env")
}

// A profile can declare NVLink without running fabricmanager, in which case
// the marker directory does not exist on the node and must not be mounted.
func TestFabricMountAbsentWhenDisabled(t *testing.T) {
	h := host.New(t.TempDir())
	state := testState()
	state.Fabric.Enabled = true // NVLink, but ManagerStateDir is empty
	s := New(h)
	ctx := context.Background()
	require.NoError(t, s.Stage(ctx, state))
	require.NoError(t, s.Apply(ctx, state))

	data, err := os.ReadFile(h.RunPath(nvidiaSpecFile))
	require.NoError(t, err)
	var spec cdiSpec
	require.NoError(t, yaml.Unmarshal(data, &spec))

	for _, m := range spec.ContainerEdits.Mounts {
		require.NotEqual(t, "/var/lib/nvml-mock/fabric-state", m.HostPath)
	}
	for _, e := range spec.ContainerEdits.Env {
		require.NotContains(t, e, "MOCK_FABRICMANAGER_STATE_DIR")
	}
}

func TestRevoke(t *testing.T) {
	h := host.New(t.TempDir())
	state := testState()
	s := New(h)
	ctx := context.Background()

	require.NoError(t, s.Stage(ctx, state))
	require.NoError(t, s.Apply(ctx, state))

	require.FileExists(t, h.RunPath(nvidiaSpecFile))
	require.FileExists(t, h.RunPath(nriSpecFile))

	require.NoError(t, s.Revoke(ctx))

	_, err := os.Stat(h.RunPath(nvidiaSpecFile))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(h.RunPath(nriSpecFile))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRevokeBeforeApplyIsNoop(t *testing.T) {
	s := New(host.New(t.TempDir()))
	require.NoError(t, s.Revoke(context.Background()))
}

func TestApplyIsIdempotent(t *testing.T) {
	h := host.New(t.TempDir())
	state := testState()
	s := New(h)
	ctx := context.Background()

	require.NoError(t, s.Stage(ctx, state))
	require.NoError(t, s.Apply(ctx, state))
	require.NoError(t, s.Apply(ctx, state))

	require.FileExists(t, h.RunPath(nvidiaSpecFile))
	require.FileExists(t, h.RunPath(nriSpecFile))
}
