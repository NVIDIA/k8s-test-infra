// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/kmod"
)

// testState returns a minimal State for gpudriver tests with a real engine YAML.
func testState(t *testing.T) *agent.State {
	t.Helper()
	cfgPath := filepath.Join("..", "..", "..", "pkg", "gpu", "mocknvml", "configs", "mock-nvml-config-a100.yaml")
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	return &agent.State{
		Software:  agent.SoftwareVersions{DriverVersion: "550.163.01"},
		Devices:   []agent.DeviceSpec{{Index: 0, MinorNumber: 0}},
		ConfigRaw: data,
	}
}

func testHost(t *testing.T) *host.Host {
	t.Helper()
	return host.New(t.TempDir())
}

// skipUnlessRootLinux skips the test on any platform where mknod requires root.
func skipUnlessRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Skip("requires root on Linux (mknod)")
	}
}

// skipUnlessNVMLLib skips the test when the NVML shim .so is not installed.
func skipUnlessNVMLLib(t *testing.T) {
	t.Helper()
	matches, _ := filepath.Glob("/usr/local/lib/libnvidia-ml.so.*.*.*")
	if len(matches) == 0 {
		t.Skip("libnvidia-ml.so not installed")
	}
}

// ─── individual surface tests ────────────────────────────────────────────────

func TestWriteProcFS_WritesVersionAndParams(t *testing.T) {
	h := testHost(t)
	state := testState(t)

	require.NoError(t, writeProcFS(t.Context(), h, state))

	versionPath := h.RootPath("driver/proc/driver/nvidia/version")
	content, err := os.ReadFile(versionPath)
	require.NoError(t, err)
	require.Contains(t, string(content), state.Software.DriverVersion,
		"version file must contain DriverVersion")

	paramsPath := h.RootPath("driver/proc/driver/nvidia/params")
	_, err = os.Stat(paramsPath)
	require.NoError(t, err, "params file must exist")
}

func TestWriteProcFS_Idempotent(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	ctx := t.Context()

	require.NoError(t, writeProcFS(ctx, h, state))
	require.NoError(t, writeProcFS(ctx, h, state), "second call must not error")
}

func TestWriteEngineConfig_WritesBothLocations(t *testing.T) {
	h := testHost(t)
	state := testState(t)

	require.NoError(t, writeEngineConfig(t.Context(), h, state))

	for _, rel := range []string{"config/config.yaml", "driver/config/config.yaml"} {
		_, err := os.Stat(h.RootPath(rel))
		require.NoError(t, err, "%s must exist", rel)
	}
}

func TestWriteEngineConfig_EmptyConfigRawErrors(t *testing.T) {
	h := testHost(t)
	state := &agent.State{}

	err := writeEngineConfig(t.Context(), h, state)
	require.Error(t, err)
}

// GFD reads its machine type from a file, so the mock has to serve one: under
// kind the DMI path it defaults to is either absent or owned by the node image.
func TestWriteMachineType_ServesTheProductName(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	state.Devices[0].Name = "NVIDIA GB300 NVL"

	require.NoError(t, writeMachineType(t.Context(), h, state))

	data, err := os.ReadFile(filepath.Join(h.Root, machineTypeRel))
	require.NoError(t, err)
	require.Equal(t, "NVIDIA GB300 NVL\n", string(data),
		"trailing newline mirrors how the kernel renders product_name")
}

// A missing file leaves GFD on its own default, which is the better failure:
// an empty one would label the node with the empty string.
func TestWriteMachineType_NoFileWithoutAProductName(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	state.Devices[0].Name = ""

	require.NoError(t, writeMachineType(t.Context(), h, state))

	_, err := os.Stat(filepath.Join(h.Root, machineTypeRel))
	require.True(t, os.IsNotExist(err), "no product name means no file")
}

func TestWriteMachineType_NoFileWithoutDevices(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	state.Devices = nil

	require.NoError(t, writeMachineType(t.Context(), h, state))

	_, err := os.Stat(filepath.Join(h.Root, machineTypeRel))
	require.True(t, os.IsNotExist(err))
}

// The file is served through the same mount as config.yaml, so it has to sit
// beside it — a path outside driver/config would never reach a container.
func TestWriteMachineType_LandsInTheServedConfigDir(t *testing.T) {
	require.Equal(t, "driver/config", filepath.Dir(machineTypeRel))
}

func TestStageNvidiaSMI_WritesSMIScript(t *testing.T) {
	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageNvidiaSMI(t.Context(), h, state))

	script := h.RootPath("driver/usr/bin/nvidia-smi.sh")
	content, err := os.ReadFile(script)
	require.NoError(t, err)
	require.Contains(t, string(content), state.Software.DriverVersion)

	// Whether nvidia-smi is the ELF or a symlink, it must exist.
	_, err = os.Lstat(h.RootPath("driver/usr/bin/nvidia-smi"))
	require.NoError(t, err, "nvidia-smi must exist (ELF or symlink)")
}

func TestStageCUDAShim_NopWhenNoLib(t *testing.T) {
	matches, _ := filepath.Glob("/usr/local/lib/libcuda.so.*.*.*")
	if len(matches) > 0 {
		t.Skip("libcuda.so is present; this test covers the no-lib path")
	}
	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageCUDAShim(t.Context(), h, state),
		"stageCUDAShim must not error when libcuda.so is absent")
}

func TestStageNVMLShim_CopiesLibAndCreatesLinks(t *testing.T) {
	skipUnlessNVMLLib(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageNVMLShim(t.Context(), h, state))

	lib64 := h.RootPath("driver/usr/lib64")
	versioned := "libnvidia-ml.so." + state.Software.DriverVersion
	for _, name := range []string{versioned, "libnvidia-ml.so.1", "libnvidia-ml.so"} {
		_, err := os.Lstat(filepath.Join(lib64, name))
		require.NoError(t, err, "%s must exist", name)
	}
}

func TestCharDevsForDevices_UsesConfiguredMinorNumbers(t *testing.T) {
	t.Parallel()

	devices := []agent.DeviceSpec{
		{Index: 0, MinorNumber: 3},
		{Index: 1, MinorNumber: 0},
	}
	require.Equal(t, []charDev{
		{"nvidia3", 195, 3},
		{"nvidia0", 195, 0},
		{"nvidiactl", 195, 255},
		{"nvidia-uvm", 510, 0},
		{"nvidia-uvm-tools", 510, 1},
	}, charDevsForDevices(devices))
}

func TestStageCharDevs_CreatesDeviceNodes(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageCharDevs(t.Context(), h, state))

	devRoot := h.RootPath("driver/dev")
	for _, name := range []string{"nvidia0", "nvidiactl", "nvidia-uvm", "nvidia-uvm-tools"} {
		_, err := os.Stat(filepath.Join(devRoot, name))
		require.NoError(t, err, "%s chardev must exist", name)
	}
}

// The node name and the minor it is created with both come from the driver's
// numbering, not from the NVML index, so a device whose two differ gets one
// node under its minor rather than a stray node under its index.
func TestStageCharDevs_UsesConfiguredMinorNumber(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	state := testState(t)
	state.Devices = []agent.DeviceSpec{{Index: 1, MinorNumber: 3}}

	require.NoError(t, stageCharDevs(t.Context(), h, state))

	devRoot := filepath.Join(h.Root, "driver/dev")
	require.FileExists(t, filepath.Join(devRoot, "nvidia3"))
	require.NoFileExists(t, filepath.Join(devRoot, "nvidia1"))
}

// ─── Apply / Revoke ──────────────────────────────────────────────────────────

func TestApply_MirrorsStagedDriverTreeIntoRunNvidiaDriver(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()

	require.NoError(t, stageNvidiaSMI(ctx, h, state))
	require.NoError(t, writeProcFS(ctx, h, state))
	require.NoError(t, writeEngineConfig(ctx, h, state))

	require.NoError(t, sim.Apply(ctx, state))
	require.True(t, sim.Ready())

	_, err := os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
	require.NoError(t, err, "nvidia-smi must be mirrored into /run/nvidia/driver")
	_, err = os.Stat(h.RunPath("nvidia/driver/proc/driver/nvidia/version"))
	require.NoError(t, err, "procfs must be mirrored into /run/nvidia/driver")
	_, err = os.Stat(h.RunPath("nvidia/driver/config/config.yaml"))
	require.NoError(t, err, "engine config must be mirrored into /run/nvidia/driver")
}

// TestApply_DoesNotReplaceAnExistingDirectoryEntry is a regression test for the
// GPU Operator validator race: a consumer container can already have
// /run/nvidia/driver bind-mounted (e.g. auto-created empty by the container
// runtime for a hostPath mount) before Apply runs. A container's mount binds
// the directory's inode, not its path — replacing that path's directory entry
// (as a symlink swap would) orphans the mount permanently, no matter what
// Apply writes afterwards. Apply must write into whatever is already there
// instead.
func TestApply_DoesNotReplaceAnExistingDirectoryEntry(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()

	require.NoError(t, stageNvidiaSMI(ctx, h, state))
	require.NoError(t, writeProcFS(ctx, h, state))
	require.NoError(t, writeEngineConfig(ctx, h, state))

	preexisting := h.RunPath("nvidia/driver")
	require.NoError(t, os.MkdirAll(preexisting, 0o755))
	before, err := os.Stat(preexisting)
	require.NoError(t, err)

	require.NoError(t, sim.Apply(ctx, state))

	after, err := os.Stat(preexisting)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after),
		"Apply must not replace the /run/nvidia/driver directory entry")

	_, err = os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
	require.NoError(t, err, "content must still land inside the pre-existing directory")
}

func TestRevoke_RemovesMirroredContentButKeepsTheDirectory(t *testing.T) {
	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()

	require.NoError(t, stageNvidiaSMI(ctx, h, state))
	require.NoError(t, writeProcFS(ctx, h, state))
	require.NoError(t, writeEngineConfig(ctx, h, state))
	require.NoError(t, sim.Apply(ctx, state))

	require.NoError(t, sim.Revoke(ctx))

	_, err := os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
	require.ErrorIs(t, err, os.ErrNotExist, "mirrored content must be removed")

	// The directory entry itself must survive: removing it would reintroduce
	// the same mount-orphaning risk Apply avoids.
	_, err = os.Stat(h.RunPath("nvidia/driver"))
	require.NoError(t, err, "the /run/nvidia/driver directory must survive Revoke")
}

func TestRevoke_IdempotentWhenContentAbsent(t *testing.T) {
	sim := New(testHost(t))

	require.NoError(t, sim.Revoke(t.Context()), "Revoke with nothing staged must not error")
}

// TestRevoke_LeavesForeignPaths covers what Revoke must not delete: /run/nvidia
// is shared with the GPU Operator, so only content Apply could have mirrored
// in is ours to remove.
func TestRevoke_LeavesForeignPaths(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, path string)
	}{
		{"empty directory", func(t *testing.T, path string) {
			require.NoError(t, os.MkdirAll(path, 0o755))
		}},
		{"regular file", func(t *testing.T, path string) {
			require.NoError(t, fsutil.Write(path, []byte("driver"), 0o644))
		}},
		{"symlink to another driver root", func(t *testing.T, path string) {
			require.NoError(t, fsutil.Symlink("/opt/real-driver", path))
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := testHost(t)
			path := h.RunPath("nvidia/driver")
			c.plant(t, path)

			require.NoError(t, New(h).Revoke(t.Context()))

			_, err := os.Lstat(path)
			require.NoError(t, err, "Revoke must leave a path it did not create")
		})
	}
}

// TestApply_ReplacesForeignNonDirectoryPaths covers the other side of that
// contract: unlike Revoke, Apply must guarantee /run/nvidia/driver is a usable
// directory going forward, so a file or a symlink from some other owner is
// replaced (a pre-existing directory is the one thing Apply never replaces —
// see TestApply_DoesNotReplaceAnExistingDirectoryEntry).
func TestApply_ReplacesForeignNonDirectoryPaths(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, path string)
	}{
		{"regular file", func(t *testing.T, path string) {
			require.NoError(t, fsutil.Write(path, []byte("driver"), 0o644))
		}},
		{"symlink to another driver root", func(t *testing.T, path string) {
			require.NoError(t, fsutil.Symlink("/opt/real-driver", path))
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := testHost(t)
			state := testState(t)
			ctx := t.Context()
			path := h.RunPath("nvidia/driver")

			require.NoError(t, stageNvidiaSMI(ctx, h, state))
			require.NoError(t, writeProcFS(ctx, h, state))
			require.NoError(t, writeEngineConfig(ctx, h, state))
			c.plant(t, path)

			require.NoError(t, New(h).Apply(ctx, state))

			fi, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, fi.IsDir(), "Apply must replace a foreign non-directory with a directory it can mirror into")

			_, err = os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
			require.NoError(t, err)
		})
	}
}

// ─── Discard ─────────────────────────────────────────────────────────────────

func TestDiscard_NopWhenNotReady(t *testing.T) {
	sim := New(testHost(t))

	// ready is false by default — Discard must be a no-op.
	require.NoError(t, sim.Discard(t.Context()))
}

// ─── full Stage (Linux root + NVML lib required) ─────────────────────────────

func TestStage_WritesAllSurfaces(t *testing.T) {
	skipUnlessRootLinux(t)
	skipUnlessNVMLLib(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)

	require.NoError(t, sim.Stage(t.Context(), state))
	require.False(t, sim.Ready(), "Stage does not mirror the driver tree into /run/nvidia/driver")
	require.NoError(t, sim.Apply(t.Context(), state))
	require.True(t, sim.Ready())

	// chardevs
	devRoot := h.RootPath("driver/dev")
	_, err := os.Stat(filepath.Join(devRoot, "nvidiactl"))
	require.NoError(t, err)

	// mirrored into /run/nvidia/driver
	_, err = os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
	require.NoError(t, err)

	// NVML shim
	_, err = os.Lstat(h.RootPath("driver/usr/lib64", "libnvidia-ml.so.1"))
	require.NoError(t, err)

	// nvidia-smi
	_, err = os.Lstat(h.RootPath("driver/usr/bin/nvidia-smi"))
	require.NoError(t, err)

	// procfs
	_, err = os.Stat(h.RootPath("driver/proc/driver/nvidia/version"))
	require.NoError(t, err)

	// engine config
	_, err = os.Stat(h.RootPath("config/config.yaml"))
	require.NoError(t, err)
}

func TestStage_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)
	skipUnlessNVMLLib(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)

	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state), "second Stage must not error")
}

// TestPruneGPUNodes_RemovesShrunkDeviceSet exercises pruneGPUNodes directly with
// plain files: pruning selects purely by name, and mknod needs root, which CI
// runners do not have.
func TestPruneGPUNodes_RemovesShrunkDeviceSet(t *testing.T) {
	devRoot := t.TempDir()
	for _, n := range []string{
		"nvidia0", "nvidia1", "nvidia2", "nvidia3",
		"nvidiactl", "nvidia-uvm", "nvidia-uvm-tools",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(devRoot, n), nil, 0o600))
	}
	// The imex simulator stages the IMEX channel tree in this same directory.
	imex := filepath.Join(devRoot, "nvidia-caps-imex-channels")
	require.NoError(t, os.MkdirAll(imex, 0o755))

	// The device set shrank from four GPUs to two.
	wanted := map[string]bool{
		"nvidia0": true, "nvidia1": true,
		"nvidiactl": true, "nvidia-uvm": true, "nvidia-uvm-tools": true,
	}
	require.NoError(t, pruneGPUNodes(devRoot, wanted))

	for _, keep := range []string{"nvidia0", "nvidia1", "nvidiactl", "nvidia-uvm", "nvidia-uvm-tools"} {
		require.FileExists(t, filepath.Join(devRoot, keep))
	}
	for _, gone := range []string{"nvidia2", "nvidia3"} {
		require.NoFileExists(t, filepath.Join(devRoot, gone),
			"stale GPU node %s must be pruned", gone)
	}
	// The nvidia prefix alone must not be grounds for deletion — this tree
	// belongs to the imex simulator, not to gpudriver.
	require.DirExists(t, imex, "IMEX channel tree must survive pruning")
}

// TestStageCharDevs_PrunesShrunkDeviceSet guards the call site, not just the
// helper: stageCharDevs must prune GPU nodes a larger device set left behind.
func TestStageCharDevs_PrunesShrunkDeviceSet(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	devRoot := h.RootPath("driver/dev")
	require.NoError(t, os.MkdirAll(devRoot, 0o755))

	// A previous, larger device set left four GPU nodes behind.
	for i, n := range []string{"nvidia0", "nvidia1", "nvidia2", "nvidia3"} {
		require.NoError(t, fsutil.Mknod(filepath.Join(devRoot, n), 195, uint32(i)))
	}

	state := &agent.State{Devices: []agent.DeviceSpec{
		{Index: 0, MinorNumber: 0},
		{Index: 1, MinorNumber: 1},
	}}
	require.NoError(t, stageCharDevs(t.Context(), h, state))

	require.FileExists(t, filepath.Join(devRoot, "nvidia0"))
	require.FileExists(t, filepath.Join(devRoot, "nvidia1"))
	require.NoFileExists(t, filepath.Join(devRoot, "nvidia2"))
	require.NoFileExists(t, filepath.Join(devRoot, "nvidia3"))
}

const hostProcModulesLine = "xfs 1556480 2 - Live 0x0000000000000000\n"

func writeKernelModule(t *testing.T, h *host.Host, name string, attrs map[string]string) {
	t.Helper()
	dir := h.SysPath(kernelSysModuleDir, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for attr, val := range attrs {
		require.NoError(t, os.WriteFile(filepath.Join(dir, attr), []byte(val), 0o444))
	}
}

// withProcModules points the module reader at a fixture and restores it, the
// way the ib and pcibus stage tests retarget their own host paths.
func withProcModules(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "modules")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	retargetProcModules(t, path)
}

func retargetProcModules(t *testing.T, path string) {
	t.Helper()
	orig := kernelProcModules
	kernelProcModules = path
	t.Cleanup(func() { kernelProcModules = orig })
}

func TestWriteKernelModules_WritesBothSurfaces(t *testing.T) {
	h := testHost(t)
	withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "xfs", map[string]string{"refcnt": "2\n", "coresize": "1556480\n"})

	state := testState(t)
	require.NoError(t, writeKernelModules(t.Context(), h, state))

	procModules, err := os.ReadFile(filepath.Join(h.Root, kmod.ProcModulesRelPath))
	require.NoError(t, err)
	require.Contains(t, string(procModules), "xfs 1556480 2")
	require.Contains(t, string(procModules), "nvidia 62312448 1 nvidia_uvm,")

	refcnt, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
	require.NoError(t, err)
	require.Equal(t, "1\n", string(refcnt))

	version, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "version"))
	require.NoError(t, err)
	require.Equal(t, state.Software.DriverVersion+"\n", string(version))

	hostRefcnt, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, "xfs", "refcnt"))
	require.NoError(t, err)
	require.Equal(t, "2\n", string(hostRefcnt),
		"a host module must keep its own refcnt so lsmod columns stay correct")
}

func TestWriteKernelModules_KeepsBuiltInModules(t *testing.T) {
	h := testHost(t)
	withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "block", nil)
	require.NoError(t, os.MkdirAll(h.SysPath(kernelSysModuleDir, "block", "parameters"), 0o755))
	require.NoError(t, os.WriteFile(
		h.SysPath(kernelSysModuleDir, "block", "parameters", "events_dfl_poll_msecs"), []byte("0\n"), 0o444))

	require.NoError(t, writeKernelModules(t.Context(), h, testState(t)))

	param, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, "block", "parameters", "events_dfl_poll_msecs"))
	require.NoError(t, err)
	require.Equal(t, "0\n", string(param))

	procModules, err := os.ReadFile(filepath.Join(h.Root, kmod.ProcModulesRelPath))
	require.NoError(t, err)
	require.NotContains(t, string(procModules), "block ",
		"a built-in module has no /proc/modules line")
}

func TestWriteKernelModules_ToleratesAnAbsentSource(t *testing.T) {
	h := testHost(t)
	retargetProcModules(t, filepath.Join(t.TempDir(), "absent"))

	require.NoError(t, writeKernelModules(t.Context(), h, testState(t)))

	require.FileExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
	require.FileExists(t, filepath.Join(h.Root, kmod.ProcModulesRelPath))
	require.FileExists(t, filepath.Join(h.Root, kmod.LsmodRelPath))
}

func TestWriteKernelModules_ConvergesWhenAHostModuleUnloads(t *testing.T) {
	h := testHost(t)
	withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "xfs", map[string]string{"refcnt": "2\n", "coresize": "1556480\n"})

	require.NoError(t, writeKernelModules(t.Context(), h, testState(t)))
	require.DirExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, "xfs"))

	require.NoError(t, os.RemoveAll(h.SysPath(kernelSysModuleDir, "xfs")))
	require.NoError(t, writeKernelModules(t.Context(), h, testState(t)))

	require.NoDirExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, "xfs"))
	require.FileExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
}

func TestDiscard_ClearsEveryModuleSurface(t *testing.T) {
	h := testHost(t)
	withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "xfs", map[string]string{"coresize": "1556480\n"})

	sim := New(h)
	require.NoError(t, writeKernelModules(t.Context(), h, testState(t)))
	sim.ready.Store(true)

	moduleTree := filepath.Join(h.Root, kmod.SysModuleRelPath)
	before, err := os.Stat(moduleTree)
	require.NoError(t, err)

	require.NoError(t, sim.Discard(t.Context()))

	after, err := os.Stat(moduleTree)
	require.NoError(t, err, "the mount source directory must outlive Discard")
	require.True(t, os.SameFile(before, after), "a served container holds this inode")

	entries, err := os.ReadDir(moduleTree)
	require.NoError(t, err)
	require.Empty(t, entries, "everything below it goes")

	require.NoFileExists(t, filepath.Join(h.Root, kmod.ProcModulesRelPath),
		"proc/modules is served by the redirect, not a mount, so Discard removes it")
	require.NoFileExists(t, filepath.Join(h.Root, kmod.LsmodRelPath),
		"the script goes with the other staged files, as nvidia-smi does")
}
