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

// loadedHardware stands in for the mokka module's configfs tree as the module
// leaves it when it loads: no GPUs, generation zero. A plain directory cannot
// drop a GPU's attributes on rmdir as configfs does, so the simulator removes
// GPUs the plain way here.
func loadedHardware(t *testing.T, sim *Simulator, h *host.Host) string {
	t.Helper()

	root := h.SysPath("kernel", "config", kmod.HardwareConfigDir)
	require.NoError(t, os.MkdirAll(filepath.Join(root, kmod.HardwareGPUsDir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, kmod.HardwareGeneration), []byte("0\n"), 0o600))
	sim.rmdir = os.RemoveAll

	return root
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
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

	for _, source := range []string{
		"Kbuild", "Makefile", "src/main_nvidia.c", "src/main_mokka.c", "src/mokka_gpu.h",
		"src/nvidia_ioctl.c", "src/nv_ioctl_abi.h",
	} {
		require.FileExists(t, filepath.Join(dir, source))
	}

	// The directives name the path the NRI plugin mounts the tree at.
	conf := readFile(t, filepath.Join(dir, kmod.ModprobeConfFile))
	for _, module := range kmod.Modules {
		require.Contains(t, conf, "install "+module+" "+kmod.ContainerDir+"/load "+module+" $CMDLINE_OPTS\n")
	}

	// The linker directive names the directory the NRI plugin mounts the mock
	// NVML in.
	require.Contains(t, readFile(t, filepath.Join(dir, kmod.LdSoConfFile)), "\n"+kmod.ContainerNVMLDir+"\n")
}

func TestSyncWaitsForTheModule(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))

	var failure string
	require.Zero(t, sim.sync(0, &failure), "nothing to describe the GPUs to before the driver container loads the module")
	require.Empty(t, failure)
	require.NoDirExists(t, h.SysPath("kernel", "config", kmod.HardwareConfigDir))
}

func TestSyncDescribesTheGPUs(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))
	root := loadedHardware(t, sim, h)

	var failure string
	applied := sim.sync(0, &failure)
	require.Empty(t, failure)
	require.NotZero(t, applied)

	// The canonical location names the directory, and the device ID is the
	// device half.
	gpu := filepath.Join(root, kmod.HardwareGPUsDir, "0000:0b:00.0")
	require.Equal(t, "GPU-b300b300-0000-0000-0000-000000000001\n", readFile(t, filepath.Join(gpu, kmod.GPUUUID)))
	require.Equal(t, "NVIDIA GB300 NVL\n", readFile(t, filepath.Join(gpu, kmod.GPUName)))
	require.Equal(t, "1\n", readFile(t, filepath.Join(gpu, kmod.GPUMinor)))
	require.Equal(t, "31c2\n", readFile(t, filepath.Join(gpu, kmod.GPUDeviceID)))
	require.Equal(t, "1\n", readFile(t, filepath.Join(gpu, kmod.GPULive)))

	// The generation goes up last, telling the loader the GPUs are all there.
	require.Equal(t, "1\n", readFile(t, filepath.Join(root, kmod.HardwareGeneration)))

	// With nothing new, the next poll leaves the tree alone.
	require.Equal(t, applied, sim.sync(applied, &failure))
	require.Equal(t, "1\n", readFile(t, filepath.Join(root, kmod.HardwareGeneration)))
}

func TestSyncFollowsAProfileChange(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))
	root := loadedHardware(t, sim, h)

	var failure string
	applied := sim.sync(0, &failure)

	changed := twoGPUs()
	changed.Devices = changed.Devices[:1]
	changed.Devices[0].UUID = "GPU-b300b300-0000-0000-0000-00000000000a"
	require.NoError(t, sim.Stage(t.Context(), changed))
	require.NotEqual(t, applied, sim.sync(applied, &failure))
	require.Empty(t, failure)

	gpus := filepath.Join(root, kmod.HardwareGPUsDir)
	require.NoDirExists(t, filepath.Join(gpus, "0000:0b:00.0"), "a GPU that goes away is unplugged and removed")
	require.Equal(t, "GPU-b300b300-0000-0000-0000-00000000000a\n",
		readFile(t, filepath.Join(gpus, "0000:0a:00.0", kmod.GPUUUID)))
	require.Equal(t, "1\n", readFile(t, filepath.Join(gpus, "0000:0a:00.0", kmod.GPULive)))
	require.Equal(t, "2\n", readFile(t, filepath.Join(root, kmod.HardwareGeneration)))
}

// A module reloaded since the last description comes back with no GPUs and at
// generation zero. The next poll describes them again, though the State has not
// changed.
func TestSyncDescribesAReloadedModule(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))
	root := loadedHardware(t, sim, h)

	var failure string
	applied := sim.sync(0, &failure)

	require.NoError(t, os.RemoveAll(root))
	loadedHardware(t, sim, h)
	require.Equal(t, applied, sim.sync(applied, &failure))
	require.Empty(t, failure)

	require.DirExists(t, filepath.Join(root, kmod.HardwareGPUsDir, "0000:0a:00.0"))
	require.Equal(t, "1\n", readFile(t, filepath.Join(root, kmod.HardwareGeneration)))
}

func TestGPUsOfNamesGPUsByCanonicalLocation(t *testing.T) {
	t.Parallel()

	gpus := gpusOf([]agent.DeviceSpec{
		{MinorNumber: 0, PCIBusID: "00000000:0A:00.0", PCIDeviceID: 0x233010DE, UUID: "GPU-a"},
		{MinorNumber: 1, PCIBusID: "not a location", UUID: "GPU-b"},
	})
	require.Equal(t, []gpu{{BusID: "0000:0a:00.0", UUID: "GPU-a", Minor: 0, DeviceID: 0x2330}}, gpus)
}

// Discard takes the staged tree away but leaves the GPUs plugged in: the next
// agent takes the description over, and the driver keeps its GPUs meanwhile.
func TestDiscardLeavesTheGPUsPluggedIn(t *testing.T) {
	t.Parallel()

	h := host.New(t.TempDir())
	sim := New(h)
	require.NoError(t, sim.Stage(t.Context(), twoGPUs()))
	root := loadedHardware(t, sim, h)

	var failure string
	sim.sync(0, &failure)
	require.NoError(t, sim.Discard(t.Context()))

	require.NoDirExists(t, h.RootPath(kmod.StagedDir))
	require.Equal(t, "1\n", readFile(t, filepath.Join(root, kmod.HardwareGPUsDir, "0000:0a:00.0", kmod.GPULive)))
}
