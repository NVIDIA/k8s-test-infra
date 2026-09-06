// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"context"
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

	require.NoError(t, writeProcFS(context.Background(), h, state))

	versionPath := filepath.Join(h.Root, "driver/proc/driver/nvidia/version")
	content, err := os.ReadFile(versionPath)
	require.NoError(t, err)
	require.Contains(t, string(content), state.Software.DriverVersion,
		"version file must contain DriverVersion")

	paramsPath := filepath.Join(h.Root, "driver/proc/driver/nvidia/params")
	_, err = os.Stat(paramsPath)
	require.NoError(t, err, "params file must exist")
}

func TestWriteProcFS_Idempotent(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	ctx := context.Background()

	require.NoError(t, writeProcFS(ctx, h, state))
	require.NoError(t, writeProcFS(ctx, h, state), "second call must not error")
}

func TestWriteEngineConfig_WritesBothLocations(t *testing.T) {
	h := testHost(t)
	state := testState(t)

	require.NoError(t, writeEngineConfig(context.Background(), h, state))

	for _, rel := range []string{"config/config.yaml", "driver/config/config.yaml"} {
		_, err := os.Stat(filepath.Join(h.Root, rel))
		require.NoError(t, err, "%s must exist", rel)
	}
}

func TestWriteEngineConfig_EmptyConfigRawErrors(t *testing.T) {
	h := testHost(t)
	state := &agent.State{}

	err := writeEngineConfig(context.Background(), h, state)
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

	require.NoError(t, stageNvidiaSMI(context.Background(), h, state))

	script := filepath.Join(h.Root, "driver/usr/bin/nvidia-smi.sh")
	content, err := os.ReadFile(script)
	require.NoError(t, err)
	require.Contains(t, string(content), state.Software.DriverVersion)

	// Whether nvidia-smi is the ELF or a symlink, it must exist.
	_, err = os.Lstat(filepath.Join(h.Root, "driver/usr/bin/nvidia-smi"))
	require.NoError(t, err, "nvidia-smi must exist (ELF or symlink)")
}

func TestStageCUDAShim_NopWhenNoLib(t *testing.T) {
	matches, _ := filepath.Glob("/usr/local/lib/libcuda.so.*.*.*")
	if len(matches) > 0 {
		t.Skip("libcuda.so is present; this test covers the no-lib path")
	}
	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageCUDAShim(context.Background(), h, state),
		"stageCUDAShim must not error when libcuda.so is absent")
}

func TestStageNVMLShim_CopiesLibAndCreatesLinks(t *testing.T) {
	skipUnlessNVMLLib(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageNVMLShim(context.Background(), h, state))

	lib64 := filepath.Join(h.Root, "driver/usr/lib64")
	versioned := "libnvidia-ml.so." + state.Software.DriverVersion
	for _, name := range []string{versioned, "libnvidia-ml.so.1", "libnvidia-ml.so"} {
		_, err := os.Lstat(filepath.Join(lib64, name))
		require.NoError(t, err, "%s must exist", name)
	}
}

func TestStageCharDevs_CreatesDeviceNodes(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	state := testState(t)

	require.NoError(t, stageCharDevs(context.Background(), h, state))

	devRoot := filepath.Join(h.Root, "driver/dev")
	for _, name := range []string{"nvidia0", "nvidiactl", "nvidia-uvm", "nvidia-uvm-tools"} {
		_, err := os.Stat(filepath.Join(devRoot, name))
		require.NoError(t, err, "%s chardev must exist", name)
	}
}

// ─── Apply / Revoke ──────────────────────────────────────────────────────────

func TestApply_CreatesSymlink(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Apply(context.Background(), h, testState(t)))
	require.True(t, sim.Ready())

	link := filepath.Join(h.Run, "nvidia/driver")
	target, err := os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, "/var/lib/nvml-mock/driver", target)
}

func TestRevoke_RemovesSymlink(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Apply(context.Background(), h, testState(t)))
	require.NoError(t, sim.Revoke(context.Background(), h))

	link := filepath.Join(h.Run, "nvidia/driver")
	_, err := os.Lstat(link)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRevoke_IdempotentWhenLinkAbsent(t *testing.T) {
	h := testHost(t)
	sim := New()

	require.NoError(t, sim.Revoke(context.Background(), h), "Revoke on absent symlink must not error")
}

// ─── Discard ─────────────────────────────────────────────────────────────────

func TestDiscard_NopWhenNotReady(t *testing.T) {
	h := testHost(t)
	sim := New()

	// ready is false by default — Discard must be a no-op.
	require.NoError(t, sim.Discard(context.Background(), h))
}

// ─── full Stage (Linux root + NVML lib required) ─────────────────────────────

func TestStage_WritesAllSurfaces(t *testing.T) {
	skipUnlessRootLinux(t)
	skipUnlessNVMLLib(t)

	h := testHost(t)
	sim := New()
	state := testState(t)

	require.NoError(t, sim.Stage(context.Background(), h, state))
	require.False(t, sim.Ready(), "Stage does not publish the driver symlink")
	require.NoError(t, sim.Apply(context.Background(), h, state))
	require.True(t, sim.Ready())

	// chardevs
	devRoot := filepath.Join(h.Root, "driver/dev")
	_, err := os.Stat(filepath.Join(devRoot, "nvidiactl"))
	require.NoError(t, err)

	// NVML shim
	_, err = os.Lstat(filepath.Join(h.Root, "driver/usr/lib64", "libnvidia-ml.so.1"))
	require.NoError(t, err)

	// nvidia-smi
	_, err = os.Lstat(filepath.Join(h.Root, "driver/usr/bin/nvidia-smi"))
	require.NoError(t, err)

	// procfs
	_, err = os.Stat(filepath.Join(h.Root, "driver/proc/driver/nvidia/version"))
	require.NoError(t, err)

	// engine config
	_, err = os.Stat(filepath.Join(h.Root, "config/config.yaml"))
	require.NoError(t, err)
}

func TestStage_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)
	skipUnlessNVMLLib(t)

	h := testHost(t)
	sim := New()
	state := testState(t)

	require.NoError(t, sim.Stage(context.Background(), h, state))
	require.NoError(t, sim.Stage(context.Background(), h, state), "second Stage must not error")
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
	devRoot := filepath.Join(h.Root, "driver/dev")
	require.NoError(t, os.MkdirAll(devRoot, 0o755))

	// A previous, larger device set left four GPU nodes behind.
	for i, n := range []string{"nvidia0", "nvidia1", "nvidia2", "nvidia3"} {
		require.NoError(t, fsutil.Mknod(filepath.Join(devRoot, n), 195, uint32(i)))
	}

	state := &agent.State{Devices: []agent.DeviceSpec{{Index: 0}, {Index: 1}}}
	require.NoError(t, stageCharDevs(context.Background(), h, state))

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

func withProcModules(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "modules")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestWriteKernelModules_WritesBothSurfaces(t *testing.T) {
	h := testHost(t)
	procModulesPath := withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "xfs", map[string]string{"refcnt": "2\n", "coresize": "1556480\n"})

	state := testState(t)
	require.NoError(t, writeKernelModules(context.Background(), h, state, procModulesPath))

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
	procModulesPath := withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "block", nil)
	require.NoError(t, os.MkdirAll(h.SysPath(kernelSysModuleDir, "block", "parameters"), 0o755))
	require.NoError(t, os.WriteFile(
		h.SysPath(kernelSysModuleDir, "block", "parameters", "events_dfl_poll_msecs"), []byte("0\n"), 0o444))

	require.NoError(t, writeKernelModules(context.Background(), h, testState(t), procModulesPath))

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
	absent := filepath.Join(t.TempDir(), "absent")

	require.NoError(t, writeKernelModules(context.Background(), h, testState(t), absent))

	require.FileExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
	require.FileExists(t, filepath.Join(h.Root, kmod.ProcModulesRelPath))
	require.FileExists(t, filepath.Join(h.Root, kmod.LsmodRelPath))
}

func TestWriteKernelModules_ConvergesWhenAHostModuleUnloads(t *testing.T) {
	h := testHost(t)
	procModulesPath := withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "xfs", map[string]string{"refcnt": "2\n", "coresize": "1556480\n"})

	require.NoError(t, writeKernelModules(context.Background(), h, testState(t), procModulesPath))
	require.DirExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, "xfs"))

	require.NoError(t, os.RemoveAll(h.SysPath(kernelSysModuleDir, "xfs")))
	require.NoError(t, writeKernelModules(context.Background(), h, testState(t), procModulesPath))

	require.NoDirExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, "xfs"))
	require.FileExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
}

func TestDiscard_ClearsEveryModuleSurface(t *testing.T) {
	h := testHost(t)
	procModulesPath := withProcModules(t, hostProcModulesLine)
	writeKernelModule(t, h, "xfs", map[string]string{"coresize": "1556480\n"})

	sim := New()
	require.NoError(t, writeKernelModules(context.Background(), h, testState(t), procModulesPath))
	sim.ready.Store(true)

	moduleTree := filepath.Join(h.Root, kmod.SysModuleRelPath)
	before, err := os.Stat(moduleTree)
	require.NoError(t, err)

	require.NoError(t, sim.Discard(context.Background(), h))

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
