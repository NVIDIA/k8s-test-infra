// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cdi

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/pkg/fmcoord"
)

func twoGPUState() *agent.State {
	return &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, UUID: "GPU-aaa"},
			{Index: 1, UUID: "GPU-bbb"},
		},
	}
}

// --- buildNvidiaSpec ---

func TestNvidiaSpecHeader(t *testing.T) {
	spec := buildNvidiaSpec(twoGPUState())
	require.Equal(t, "0.6.0", spec.CDIVersion)
	require.Equal(t, "nvidia.com/gpu", spec.Kind)
}

func TestNvidiaSpecSharedDeviceNodes(t *testing.T) {
	spec := buildNvidiaSpec(twoGPUState())
	require.NotNil(t, spec.ContainerEdits)

	paths := make([]string, 0, len(spec.ContainerEdits.DeviceNodes))
	for _, dn := range spec.ContainerEdits.DeviceNodes {
		paths = append(paths, dn.Path)
		// hostPath must point at the overlay root, not the agent container's /host prefix.
		require.Contains(t, dn.HostPath, overlayHostRoot)
	}
	require.ElementsMatch(t, []string{"/dev/nvidiactl", "/dev/nvidia-uvm", "/dev/nvidia-uvm-tools"}, paths)
}

func TestNvidiaSpecMounts(t *testing.T) {
	spec := buildNvidiaSpec(twoGPUState())
	require.NotNil(t, spec.ContainerEdits)

	containerPaths := make([]string, 0, len(spec.ContainerEdits.Mounts))
	for _, m := range spec.ContainerEdits.Mounts {
		containerPaths = append(containerPaths, m.ContainerPath)
		require.Contains(t, m.HostPath, overlayHostRoot)
	}
	require.ElementsMatch(t, []string{
		"/usr/lib64/libnvidia-ml.so.1",
		"/usr/bin/nvidia-smi",
		"/etc/nvml-mock",
	}, containerPaths)
}

func TestNvidiaSpecHookAndEnv(t *testing.T) {
	spec := buildNvidiaSpec(twoGPUState())
	require.NotNil(t, spec.ContainerEdits)

	require.Len(t, spec.ContainerEdits.Hooks, 1)
	require.Equal(t, "createContainer", spec.ContainerEdits.Hooks[0].HookName)

	require.Contains(t, spec.ContainerEdits.Env, "NVIDIA_VISIBLE_DEVICES=void")
	require.Contains(t, spec.ContainerEdits.Env, "MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml")
	require.Contains(t, spec.ContainerEdits.Env, "MOCK_NVML_OVERRIDES=/etc/nvml-mock/overrides.yaml")
}

func TestNvidiaSpecPerGPUDevices(t *testing.T) {
	// 2 GPUs × (index + UUID) + "all" = 5
	spec := buildNvidiaSpec(twoGPUState())
	require.Len(t, spec.Devices, 5)
	require.Equal(t, "0", spec.Devices[0].Name)
	require.Equal(t, "GPU-aaa", spec.Devices[1].Name)
	require.Equal(t, "1", spec.Devices[2].Name)
	require.Equal(t, "GPU-bbb", spec.Devices[3].Name)
	require.Equal(t, "all", spec.Devices[4].Name)

	// Each per-GPU entry has exactly one device node pointing at the right index.
	require.Equal(t, "/dev/nvidia0", spec.Devices[0].ContainerEdits.DeviceNodes[0].Path)
	require.Equal(t, "/dev/nvidia1", spec.Devices[2].ContainerEdits.DeviceNodes[0].Path)

	// Index and UUID entries for the same GPU share the same device node.
	require.Equal(t, spec.Devices[0].ContainerEdits.DeviceNodes, spec.Devices[1].ContainerEdits.DeviceNodes)
	require.Equal(t, spec.Devices[2].ContainerEdits.DeviceNodes, spec.Devices[3].ContainerEdits.DeviceNodes)
}

func TestNvidiaSpecAllDevice(t *testing.T) {
	spec := buildNvidiaSpec(twoGPUState())
	all := spec.Devices[len(spec.Devices)-1]
	require.Equal(t, "all", all.Name)
	// "all" aggregates the per-GPU nodes only (control nodes are in containerEdits).
	require.Len(t, all.ContainerEdits.DeviceNodes, 2)
}

func TestNvidiaSpecNoUUIDEntry(t *testing.T) {
	state := &agent.State{Devices: []agent.DeviceSpec{{Index: 0, UUID: ""}}}
	spec := buildNvidiaSpec(state)
	// Only index entry + "all" — no UUID entry when UUID is empty.
	require.Len(t, spec.Devices, 2)
	require.Equal(t, "0", spec.Devices[0].Name)
	require.Equal(t, "all", spec.Devices[1].Name)
}

// The mounted path follows the configured state dir, which the chart allows to
// differ from the default.
func TestNvidiaSpecFabricManagerEnabled(t *testing.T) {
	const stateDir = "/var/lib/custom/fabric-state"
	state := twoGPUState()
	state.Fabric.ManagerStateDir = stateDir
	spec := buildNvidiaSpec(state)

	var hasFabricMount bool
	for _, m := range spec.ContainerEdits.Mounts {
		if m.HostPath == stateDir {
			hasFabricMount = true
			require.Equal(t, stateDir, m.ContainerPath)
		}
	}
	require.True(t, hasFabricMount, "fabric-state mount missing")
	require.Contains(t, spec.ContainerEdits.Env, "MOCK_FABRICMANAGER_STATE_DIR="+stateDir)
}

// NVLink alone leaves no marker directory on the node, so nothing is mounted.
func TestNvidiaSpecFabricManagerDisabled(t *testing.T) {
	state := twoGPUState()
	state.Fabric.Enabled = true
	spec := buildNvidiaSpec(state)

	for _, m := range spec.ContainerEdits.Mounts {
		require.NotEqual(t, fmcoord.DefaultStateDir, m.HostPath)
	}
	for _, e := range spec.ContainerEdits.Env {
		require.NotContains(t, e, "MOCK_FABRICMANAGER_STATE_DIR")
	}
}

// --- buildNRISpec ---

func TestNRISpecHeader(t *testing.T) {
	spec := buildNRISpec(twoGPUState())
	require.Equal(t, "0.6.0", spec.CDIVersion)
	require.Equal(t, "nvml-mock.nvidia.com/gpu", spec.Kind)
}

func TestNRISpecEnv(t *testing.T) {
	spec := buildNRISpec(twoGPUState())
	require.NotNil(t, spec.ContainerEdits)
	require.Contains(t, spec.ContainerEdits.Env, "NVML_MOCK_DEVICE_SOURCE=cdi")
	// No library mounts or hooks — the NRI overlay bind-mount delivers those.
	require.Empty(t, spec.ContainerEdits.Mounts)
	require.Empty(t, spec.ContainerEdits.Hooks)
}

func TestNRISpecPerGPUDevices(t *testing.T) {
	// Index entries only (no UUID) + "all" = 3.
	spec := buildNRISpec(twoGPUState())
	require.Len(t, spec.Devices, 3)
	require.Equal(t, "0", spec.Devices[0].Name)
	require.Equal(t, "1", spec.Devices[1].Name)
	require.Equal(t, "all", spec.Devices[2].Name)
}

func TestNRISpecAllDeviceIncludesControlNodes(t *testing.T) {
	spec := buildNRISpec(twoGPUState())
	all := spec.Devices[len(spec.Devices)-1]
	require.Equal(t, "all", all.Name)

	// 2 per-GPU + nvidiactl + nvidia-uvm + nvidia-uvm-tools = 5
	require.Len(t, all.ContainerEdits.DeviceNodes, 5)

	paths := make([]string, 0, len(all.ContainerEdits.DeviceNodes))
	for _, dn := range all.ContainerEdits.DeviceNodes {
		paths = append(paths, dn.Path)
	}
	require.Contains(t, paths, "/dev/nvidia0")
	require.Contains(t, paths, "/dev/nvidia1")
	require.Contains(t, paths, "/dev/nvidiactl")
	require.Contains(t, paths, "/dev/nvidia-uvm")
	require.Contains(t, paths, "/dev/nvidia-uvm-tools")
}

func TestNRISpecHostPathsUseOverlayRoot(t *testing.T) {
	spec := buildNRISpec(twoGPUState())
	for _, d := range spec.Devices {
		for _, dn := range d.ContainerEdits.DeviceNodes {
			require.Contains(t, dn.HostPath, overlayHostRoot,
				"hostPath %q must be rooted at overlayHostRoot", dn.HostPath)
		}
	}
}
