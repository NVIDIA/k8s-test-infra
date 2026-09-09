// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cdi

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/fabricmanager"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

func twoGPUState() *agent.State {
	return &agent.State{
		Devices: []agent.DeviceSpec{
			{Index: 0, UUID: "GPU-aaa", MinorNumber: 0},
			{Index: 1, UUID: "GPU-bbb", MinorNumber: 1},
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

// The container does not only read the config directory: `nvidia-smi --gpu-reset`
// clears the device's bucket from overrides.yaml, taking an flock and rewriting
// the file. Mounted read-only, that write fails with EROFS exactly when the GPU
// has state to clear, so the reset fails on the GPUs that need it and "succeeds"
// on the healthy ones that skip the write. The driver library and nvidia-smi
// stay read-only; only the config directory is writable.
func TestNvidiaSpecConfigDirIsWritable(t *testing.T) {
	spec := buildNvidiaSpec(twoGPUState())
	require.NotNil(t, spec.ContainerEdits)

	for _, m := range spec.ContainerEdits.Mounts {
		switch m.ContainerPath {
		case "/etc/nvml-mock":
			require.Contains(t, m.Options, "rw", "the reset writes overrides.yaml through this mount")
			require.NotContains(t, m.Options, "ro")
		case "/usr/lib64/libnvidia-ml.so.1", "/usr/bin/nvidia-smi":
			require.Contains(t, m.Options, "ro", "%s must stay immutable inside the container", m.ContainerPath)
		}
	}
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
		require.NotEqual(t, fabricmanager.DefaultStateDir, m.HostPath)
	}
	for _, e := range spec.ContainerEdits.Env {
		require.NotContains(t, e, "MOCK_FABRICMANAGER_STATE_DIR")
	}
}

// --- PCI sysfs mounts ---

// pciState carries what a profile with a pcie_topology block compiles to.
func pciState() *agent.State {
	state := twoGPUState()
	state.Devices[0].PCIBusID = "0000:07:00.0"
	state.Devices[1].PCIBusID = "0000:0f:00.0"
	return state
}

func mountByContainerPath(spec cdiSpec, path string) (cdiMount, bool) {
	for _, m := range spec.ContainerEdits.Mounts {
		if m.ContainerPath == path {
			return m, true
		}
	}
	return cdiMount{}, false
}

// GFD and the DRA driver are Go, so libpcisysfs.so never sees their openat
// calls: only a real mount at the kernel path reaches them.
func TestNvidiaSpecServesPCISysfsAtKernelPaths(t *testing.T) {
	t.Parallel()

	spec := buildNvidiaSpec(pciState())

	devices, ok := mountByContainerPath(spec, "/sys/bus/pci/devices")
	require.True(t, ok, "/sys/bus/pci/devices must be served")
	require.Equal(t, overlayHostRoot+"/"+pcisysfs.PCIDevicesRelPath, devices.HostPath)
	require.Contains(t, devices.Options, "ro")

	sysDevices, ok := mountByContainerPath(spec, "/sys/devices")
	require.True(t, ok, "/sys/devices must be served")
	require.Equal(t, overlayHostRoot+"/"+pcisysfs.SysDevicesRelPath, sysDevices.HostPath)
	require.Contains(t, sysDevices.Options, "ro")
}

// The two mounts are one feature: the entries are relative symlinks into
// ../../../devices/pciDDDD:BB, so half the pair reads like no mount at all.
func TestNvidiaSpecPCISysfsMountsAreEmittedAsAPair(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		state  *agent.State
		served bool
	}{
		"a rendered tree serves both":     {state: pciState(), served: true},
		"no rendered tree serves neither": {state: twoGPUState(), served: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec := buildNvidiaSpec(tc.state)

			_, servesLookup := mountByContainerPath(spec, "/sys/bus/pci/devices")
			require.Equal(t, tc.served, servesLookup, "the lookup directory of BDF symlinks")

			_, servesHierarchy := mountByContainerPath(spec, "/sys/devices")
			require.Equal(t, tc.served, servesHierarchy, "the hierarchy those symlinks point into")
		})
	}
}

// A profile whose devices declare no bus_id renders no tree, and a CDI mount
// whose source does not exist fails container creation for the whole pod.
func TestNvidiaSpecOmitsPCISysfsWithoutTopology(t *testing.T) {
	t.Parallel()

	spec := buildNvidiaSpec(twoGPUState())

	_, ok := mountByContainerPath(spec, "/sys/bus/pci/devices")
	require.False(t, ok, "nothing may be served when no tree is rendered")
	_, ok = mountByContainerPath(spec, "/sys/devices")
	require.False(t, ok, "nothing may be served when no tree is rendered")
}

// migState is one GPU partitioned into two 1-compute-instance partitions,
// which is the shape migStrategy=single asks for.
func migState() *agent.State {
	state := twoGPUState()
	state.MIG = agent.MIGState{
		CapsMajor: 238,
		GPUs: []agent.MIGGPU{{
			Minor: 0,
			GPUInstances: []agent.MIGGPUInstance{
				{ID: 0, ComputeInstances: []agent.MIGComputeInstance{{ID: 0, UUID: "MIG-aaa-0"}}},
				{ID: 1, ComputeInstances: []agent.MIGComputeInstance{{ID: 0, UUID: "MIG-aaa-1"}}},
			},
		}},
	}
	return state
}

// A MIG partition is allocated through the same channel a whole GPU is: the
// device plugin reports the UUID NVML gave it, and the container runtime
// resolves that name against this spec. Without an entry the resolution fails
// outright — "unresolvable CDI devices nvidia.com/gpu=MIG-..." — so a pod that
// was scheduled onto a partition never starts, which is how this surfaced.
func TestNvidiaSpecMigDeviceEntries(t *testing.T) {
	t.Parallel()

	spec := buildNvidiaSpec(migState())

	for _, uuid := range []string{"MIG-aaa-0", "MIG-aaa-1"} {
		dev, ok := deviceByName(spec, uuid)
		require.True(t, ok, "no CDI entry names partition %s", uuid)
		// The partition's compute capacity is still reached through its
		// parent's node; the cap nodes only guard access to it.
		require.Contains(t, nodePaths(dev), "/dev/nvidia0")
	}
}

// The cap minors here are the ones migcaps allocates for this layout, and the
// two have to agree: the runtime injects whatever node this spec names, so a
// minor that does not match the staged table hands the container the chardev
// guarding a different partition.
func TestNvidiaSpecMigCapNodes(t *testing.T) {
	t.Parallel()

	spec := buildNvidiaSpec(migState())

	// config=1 and monitor=2 are reserved, so gi0 takes 3, its ci0 takes 4,
	// then gi1 takes 5 and its ci0 takes 6.
	first, ok := deviceByName(spec, "MIG-aaa-0")
	require.True(t, ok)
	require.ElementsMatch(t, []string{
		"/dev/nvidia0",
		"/dev/nvidia-caps/nvidia-cap3",
		"/dev/nvidia-caps/nvidia-cap4",
	}, nodePaths(first))

	second, ok := deviceByName(spec, "MIG-aaa-1")
	require.True(t, ok)
	require.ElementsMatch(t, []string{
		"/dev/nvidia0",
		"/dev/nvidia-caps/nvidia-cap5",
		"/dev/nvidia-caps/nvidia-cap6",
	}, nodePaths(second))

	// Cap nodes are staged under the overlay, not the node's real /dev.
	for _, dn := range first.ContainerEdits.DeviceNodes {
		require.Contains(t, dn.HostPath, overlayHostRoot)
	}
}

// The whole-GPU entries stay exactly as they were: a partitioned node still
// has to serve consumers that ask for a full GPU by index or UUID, and the
// non-MIG scenarios assert on those names.
func TestNvidiaSpecMigDoesNotDisturbWholeGPUEntries(t *testing.T) {
	t.Parallel()

	plain := buildNvidiaSpec(twoGPUState())
	partitioned := buildNvidiaSpec(migState())

	for _, name := range []string{"0", "GPU-aaa", "1", "GPU-bbb", "all"} {
		want, ok := deviceByName(plain, name)
		require.True(t, ok)
		got, ok := deviceByName(partitioned, name)
		require.True(t, ok, "partitioning dropped the %q entry", name)
		require.Equal(t, want, got, "partitioning changed the %q entry", name)
	}
}

// A node with MIG off must not gain a nvidia-caps node anywhere: the chardevs
// are not staged in that case, and a CDI entry naming a missing hostPath fails
// container creation for the whole pod.
func TestNvidiaSpecNoMigEntriesWhenUnpartitioned(t *testing.T) {
	t.Parallel()

	spec := buildNvidiaSpec(twoGPUState())

	for _, dev := range spec.Devices {
		for _, path := range nodePaths(dev) {
			require.NotContains(t, path, "nvidia-caps",
				"entry %q names a cap node on an unpartitioned node", dev.Name)
		}
	}
}

func deviceByName(spec cdiSpec, name string) (cdiDevice, bool) {
	for _, d := range spec.Devices {
		if d.Name == name {
			return d, true
		}
	}
	return cdiDevice{}, false
}

func nodePaths(dev cdiDevice) []string {
	paths := make([]string, 0, len(dev.ContainerEdits.DeviceNodes))
	for _, dn := range dev.ContainerEdits.DeviceNodes {
		paths = append(paths, dn.Path)
	}
	return paths
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

// The device node a container gets has to be the one the driver would have
// created for that GPU, which is named after the minor number. Naming it after
// the NVML index hands the container a different GPU's node wherever the two
// differ.
func TestNvidiaSpecPerGPUDevicesFollowMinorNumbers(t *testing.T) {
	state := &agent.State{Devices: []agent.DeviceSpec{{Index: 1, UUID: "GPU-bbb", MinorNumber: 3}}}
	spec := buildNvidiaSpec(state)

	require.Equal(t, "/dev/nvidia3", spec.Devices[0].ContainerEdits.DeviceNodes[0].Path)
	// The index and UUID entries address the same GPU, so they share the node.
	require.Equal(t, spec.Devices[0].ContainerEdits.DeviceNodes, spec.Devices[1].ContainerEdits.DeviceNodes)
}

func TestNRISpecPerGPUDevicesFollowMinorNumbers(t *testing.T) {
	state := &agent.State{Devices: []agent.DeviceSpec{{Index: 1, MinorNumber: 3}}}
	spec := buildNRISpec(state)

	require.Equal(t, "/dev/nvidia3", spec.Devices[0].ContainerEdits.DeviceNodes[0].Path)
}
