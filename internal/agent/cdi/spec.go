// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package cdi

import (
	"fmt"
	"strconv"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
)

// overlayHostRoot is the path containerd sees for the mock overlay on the host.
// CDI hostPaths are resolved by the runtime on the node itself, not by the agent container.
const overlayHostRoot = "/var/lib/nvml-mock"

// cdiSpec is the structure of a CDI 0.6.0 spec file.
type cdiSpec struct {
	CDIVersion     string      `json:"cdiVersion"`
	Kind           string      `json:"kind"`
	ContainerEdits *cdiEdits   `json:"containerEdits,omitempty"`
	Devices        []cdiDevice `json:"devices,omitempty"`
}

type cdiEdits struct {
	DeviceNodes []cdiDeviceNode `json:"deviceNodes,omitempty"`
	Mounts      []cdiMount      `json:"mounts,omitempty"`
	Hooks       []cdiHook       `json:"hooks,omitempty"`
	Env         []string        `json:"env,omitempty"`
}

type cdiDeviceNode struct {
	Path     string `json:"path"`
	HostPath string `json:"hostPath,omitempty"`
}

type cdiMount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

type cdiHook struct {
	HookName string   `json:"hookName"`
	Path     string   `json:"path"`
	Args     []string `json:"args,omitempty"`
}

type cdiDevice struct {
	Name           string   `json:"name"`
	ContainerEdits cdiEdits `json:"containerEdits"`
}

// buildNvidiaSpec returns the nvidia.com/gpu CDI spec consumed by nvidia-container-runtime.
// It includes library mounts, an ldcache hook, and per-GPU + "all" device entries.
func buildNvidiaSpec(state *agent.State) cdiSpec {
	devRoot := overlayHostRoot + "/driver/dev"

	edits := &cdiEdits{
		// Control devices are shared by every named GPU entry (per-GPU nvidia<N>
		// nodes live under devices[*].containerEdits, not here).
		DeviceNodes: []cdiDeviceNode{
			{Path: "/dev/nvidiactl", HostPath: devRoot + "/nvidiactl"},
			{Path: "/dev/nvidia-uvm", HostPath: devRoot + "/nvidia-uvm"},
			{Path: "/dev/nvidia-uvm-tools", HostPath: devRoot + "/nvidia-uvm-tools"},
		},
		Mounts: []cdiMount{
			{
				HostPath:      overlayHostRoot + "/driver/usr/lib64/libnvidia-ml.so.1",
				ContainerPath: "/usr/lib64/libnvidia-ml.so.1",
				Options:       []string{"ro", "nosuid", "nodev", "bind"},
			},
			{
				HostPath:      overlayHostRoot + "/driver/usr/bin/nvidia-smi",
				ContainerPath: "/usr/bin/nvidia-smi",
				Options:       []string{"ro", "nosuid", "nodev", "bind"},
			},
			// Bind the config directory (not just config.yaml) so atomic renames
			// by nvml-mock-ctl's overrides.yaml writes are visible inside the container.
			//
			// Writable because the container also resets through it: the mock
			// serves `nvidia-smi --gpu-reset` by clearing the device's bucket from
			// overrides.yaml, which takes an flock and rewrites the file. Read-only
			// here failed that write with EROFS exactly when the GPU had state to
			// clear, so the remediation an operator reaches for first reported
			// "GPU Reset couldn't run" on precisely the GPUs that needed it, while
			// healthy ones "succeeded" through the no-write path.
			{
				HostPath:      overlayHostRoot + "/driver/config",
				ContainerPath: "/etc/nvml-mock",
				Options:       []string{"rw", "nosuid", "nodev", "bind"},
			},
		},
		// update-ldcache rebuilds the dynamic linker cache inside the container
		// namespace so dlopen finds the bind-mounted libnvidia-ml.so.1 without
		// modifying the container image's own ld.so.conf.
		Hooks: []cdiHook{
			{
				HookName: "createContainer",
				Path:     "/usr/bin/nvidia-cdi-hook",
				Args:     []string{"nvidia-cdi-hook", "update-ldcache", "--folder", "/usr/lib64"},
			},
		},
		Env: []string{
			// void tells the container toolkit to skip its own device enumeration;
			// without it the toolkit would override our mock nodes with an empty set.
			"NVIDIA_VISIBLE_DEVICES=void",
			"MOCK_NVML_CONFIG=/etc/nvml-mock/config.yaml",
			"MOCK_NVML_OVERRIDES=/etc/nvml-mock/overrides.yaml",
		},
	}

	edits.Mounts = append(edits.Mounts, pciSysfsMounts(state)...)

	// The .so resolving fabric.state:auto runs in the consumer container, so the
	// marker dir mounts there — but only where it exists, else creation fails.
	if stateDir := state.Fabric.ManagerStateDir; stateDir != "" {
		edits.Mounts = append(edits.Mounts, cdiMount{
			HostPath:      stateDir,
			ContainerPath: stateDir,
			Options:       []string{"ro", "nosuid", "nodev", "bind"},
		})
		edits.Env = append(edits.Env, "MOCK_FABRICMANAGER_STATE_DIR="+stateDir)
	}

	allNodes := make([]cdiDeviceNode, 0, len(state.Devices))
	devices := make([]cdiDevice, 0, len(state.Devices)*2+1)
	for _, d := range state.Devices {
		node := cdiDeviceNode{
			Path:     fmt.Sprintf("/dev/nvidia%d", d.Index),
			HostPath: fmt.Sprintf("%s/nvidia%d", devRoot, d.Index),
		}
		allNodes = append(allNodes, node)
		// Index shorthand ("0".."N-1"): addressed by nvidia-container-runtime CLI.
		devices = append(devices, cdiDevice{
			Name:           strconv.Itoa(d.Index),
			ContainerEdits: cdiEdits{DeviceNodes: []cdiDeviceNode{node}},
		})
		// UUID entry: addressed by containerd and the device plugin allocator.
		if d.UUID != "" {
			devices = append(devices, cdiDevice{
				Name:           d.UUID,
				ContainerEdits: cdiEdits{DeviceNodes: []cdiDeviceNode{node}},
			})
		}
	}
	devices = append(devices, cdiDevice{
		Name:           "all",
		ContainerEdits: cdiEdits{DeviceNodes: allNodes},
	})

	return cdiSpec{
		CDIVersion:     "0.6.0",
		Kind:           "nvidia.com/gpu",
		ContainerEdits: edits,
		Devices:        devices,
	}
}

// pciSysfsMounts serves the tree the pcibus simulator renders at the kernel
// paths a Go consumer reads.
//
// libpcisysfs.so already redirects those paths for libc consumers such as lspci,
// but Go's os package issues openat directly, so the shim never sees the open
// and the process reads the node's real /sys — where the mock GPUs do not
// exist. GPU Feature Discovery resolves each GPU's BDF from NVML and then reads
// its class from sysfs, so it labelled the node nvidia.com/gpu.mode=unknown
// (#673). The NVIDIA DRA driver resolves dra.k8s.io/pcieRoot the same way.
//
// Both entries go together: the lookup directory holds relative symlinks into
// ../../../devices/pciDDDD:BB, so serving it alone yields entries that list and
// whose every attribute read returns ENOENT.
//
// /sys/devices is served whole. Narrowing it to the profile's root complexes
// would need mountpoints the runtime cannot create on a read-only sysfs, and it
// fails container creation rather than degrading — profiles routinely declare
// root complexes the node does not have. A served container therefore does not
// see the host's other device classes, CPU topology among them; #689 tracks
// removing that trade-off.
//
// Emitted only when the state declares a topology, because pcibus renders on
// exactly that condition and a mount with a missing source fails container
// creation for the whole pod.
func pciSysfsMounts(state *agent.State) []cdiMount {
	if !state.HasPCITopology() {
		return nil
	}

	mounts := make([]cdiMount, 0, 2)
	// The renderer's rel-paths are the kernel paths without the leading slash,
	// because it renders a fake root rather than a private layout.
	for _, relPath := range []string{pcisysfs.SysDevicesRelPath, pcisysfs.PCIDevicesRelPath} {
		mounts = append(mounts, cdiMount{
			HostPath:      overlayHostRoot + "/" + relPath,
			ContainerPath: "/" + relPath,
			Options:       []string{"ro", "nosuid", "nodev", "bind"},
		})
	}
	return mounts
}

// buildNRISpec returns the nvml-mock.nvidia.com/gpu CDI spec consumed by the NRI plugin's
// cdi injection mode. No hooks or library mounts: the NRI plugin already delivers those via
// the overlay bind-mount. The distinct vendor (nvml-mock.nvidia.com vs nvidia.com) keeps
// MEP-0002's "exactly one source of CDI references per container" invariant observable.
func buildNRISpec(state *agent.State) cdiSpec {
	devRoot := overlayHostRoot + "/driver/dev"

	allNodes := make([]cdiDeviceNode, 0, len(state.Devices)+3)
	devices := make([]cdiDevice, 0, len(state.Devices)+1)

	for _, d := range state.Devices {
		node := cdiDeviceNode{
			Path:     fmt.Sprintf("/dev/nvidia%d", d.Index),
			HostPath: fmt.Sprintf("%s/nvidia%d", devRoot, d.Index),
		}
		devices = append(devices, cdiDevice{
			Name:           strconv.Itoa(d.Index),
			ContainerEdits: cdiEdits{DeviceNodes: []cdiDeviceNode{node}},
		})
		allNodes = append(allNodes, node)
	}

	// "all" also covers the control devices so a workload that only requests
	// "all" still gets the UVM and nvidiactl nodes it needs.
	for _, extra := range []string{"nvidiactl", "nvidia-uvm", "nvidia-uvm-tools"} {
		allNodes = append(allNodes, cdiDeviceNode{
			Path:     "/dev/" + extra,
			HostPath: devRoot + "/" + extra,
		})
	}
	devices = append(devices, cdiDevice{
		Name:           "all",
		ContainerEdits: cdiEdits{DeviceNodes: allNodes},
	})

	return cdiSpec{
		CDIVersion: "0.6.0",
		Kind:       "nvml-mock.nvidia.com/gpu",
		// NVML_MOCK_DEVICE_SOURCE makes the injection path (CDI vs raw NRI) observable
		// from inside the container — the two modes are otherwise indistinguishable.
		ContainerEdits: &cdiEdits{Env: []string{"NVML_MOCK_DEVICE_SOURCE=cdi"}},
		Devices:        devices,
	}
}
