// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiakmod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	kmod "github.com/NVIDIA/k8s-test-infra/shims/nvidia-kmod"
)

// twoGPUs is shaped like the gb300 profile: upper-case bus IDs, and the PCI
// device ID as NVML reports it, device over vendor.
func twoGPUs() *agent.State {
	return &agent.State{Devices: []agent.DeviceSpec{
		{
			Index: 0, MinorNumber: 0, Name: "NVIDIA GB300 NVL", PCIDeviceID: 0x31C210DE,
			UUID: "GPU-b300b300-0000-0000-0000-000000000000", PCIBusID: "0000:0A:00.0",
		},
		{
			Index: 1, MinorNumber: 1, Name: "NVIDIA GB300 NVL", PCIDeviceID: 0x31C210DE,
			UUID: "GPU-b300b300-0000-0000-0000-000000000001", PCIBusID: "0000:0B:00.0",
		},
	}}
}

func TestStageWritesWhatTheDriverContainerLoads(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))
	require.True(t, sim.Ready())

	dir := h.RootPath(kmod.StagedDir)

	// modprobe runs the loader directly, and the driver container the
	// persistenced stand-in, so the mode embed dropped must be back.
	for _, executable := range kmod.Executables {
		info, err := os.Stat(filepath.Join(dir, executable))
		require.NoError(t, err)
		require.NotZero(t, info.Mode()&0o111, "%s must be executable", executable)
	}

	for _, source := range []string{"Kbuild", "Makefile", "src/main_nvidia.c", "src/nvidia_ioctl.c", "src/nv_ioctl_abi.h"} {
		require.FileExists(t, filepath.Join(dir, source))
	}

	// Lower-case locations and the device half of the PCI ID, as nvidia.ko
	// parses them.
	gpus, err := os.ReadFile(filepath.Join(dir, kmod.GPUsFile))
	require.NoError(t, err)
	require.Equal(t,
		"0000:0a:00.0|GPU-b300b300-0000-0000-0000-000000000000|NVIDIA GB300 NVL|0|31c2,"+
			"0000:0b:00.0|GPU-b300b300-0000-0000-0000-000000000001|NVIDIA GB300 NVL|1|31c2",
		string(gpus))

	// The directives name the path the NRI plugin mounts the tree at.
	conf, err := os.ReadFile(filepath.Join(dir, kmod.ModprobeConfFile))
	require.NoError(t, err)
	for _, module := range kmod.Modules {
		require.Contains(t, string(conf),
			"install "+module+" "+kmod.ContainerDir+"/load "+module+" $CMDLINE_OPTS\n")
	}

	// The linker directive names the directory the NRI plugin mounts the mock
	// NVML in.
	ldconf, err := os.ReadFile(filepath.Join(dir, kmod.LdSoConfFile))
	require.NoError(t, err)
	require.Contains(t, string(ldconf), "\n"+kmod.ContainerNVMLDir+"\n")
}

func TestStageReplacesRecordsOnProfileChange(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))

	oneGPU := twoGPUs()
	oneGPU.Devices = oneGPU.Devices[:1]
	require.NoError(t, sim.Stage(t.Context(), oneGPU))

	gpus, err := os.ReadFile(h.RootPath(kmod.StagedDir, kmod.GPUsFile))
	require.NoError(t, err)
	require.Equal(t, "0000:0a:00.0|GPU-b300b300-0000-0000-0000-000000000000|NVIDIA GB300 NVL|0|31c2", string(gpus))
}

func TestDiscardRemovesTheTree(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))
	require.NoError(t, sim.Discard(t.Context()))

	require.NoDirExists(t, h.RootPath(kmod.StagedDir))
}
