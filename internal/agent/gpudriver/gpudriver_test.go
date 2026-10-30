// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// The driver prints params from its registry table as "%s: %u\n" on the bare
// key name; NVreg_ is the module-parameter spelling (modprobe nvidia
// NVreg_...) and never appears in this file. Consumers match
// whole lines: nvidia-cdi-hook's disable-device-node-modification rewrites
// exactly "ModifyDeviceFiles: 1". An agent killed before Discard runs leaves
// the staged tree on the host, so the test starts from the file it left behind.
func TestWriteProcFS_ParamsUseDriverKeyNames(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	paramsPath := h.RootPath("driver/proc/driver/nvidia/params")
	require.NoError(t, os.MkdirAll(filepath.Dir(paramsPath), 0o755))
	require.NoError(t, os.WriteFile(paramsPath, []byte("EnableMSI: 1\nNVreg_ModifyDeviceFiles: 1\n"), 0o644))

	require.NoError(t, writeProcFS(t.Context(), h, testState(t)))

	content, err := os.ReadFile(paramsPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	for _, line := range lines {
		require.False(t, strings.HasPrefix(line, "NVreg_"),
			"params line %q uses the module-parameter spelling", line)
		// The driver's two forms: "%s: %u" for table entries, "%s: \"%s\"" for strings.
		require.Regexp(t, `^[A-Za-z][A-Za-z0-9]*: ([0-9]+|"[^"]*")$`, line,
			"params line %q is not \"<Key>: <value>\"", line)
	}
	require.Contains(t, lines, "ModifyDeviceFiles: 1")
	require.Contains(t, lines, `RegistryDwords: ""`)
}

func TestWriteProcFS_Idempotent(t *testing.T) {
	h := testHost(t)
	state := testState(t)
	ctx := t.Context()

	require.NoError(t, writeProcFS(ctx, h, state))
	require.NoError(t, writeProcFS(ctx, h, state), "second call must not error")
}

// Slurm's gres AutoDetect=nvidia enumerates /proc/driver/nvidia/gpus/<bdf> and
// reads the model, UUID and minor number from each GPU's information file.
func TestWriteProcFS_DescribesEachGPU(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)
	state.Devices = []agent.DeviceSpec{
		{Index: 0, MinorNumber: 0, PCIBusID: "0000:07:00.0", Name: "NVIDIA A100-SXM4-40GB", UUID: "GPU-aaaa"},
		{Index: 1, MinorNumber: 3, PCIBusID: "0000:0F:00.0", Name: "NVIDIA A100-SXM4-40GB", UUID: "GPU-bbbb"},
		{Index: 2, MinorNumber: 4, PCIBusID: ""},
	}

	require.NoError(t, writeProcFS(t.Context(), h, state))

	entries, err := os.ReadDir(h.RootPath("driver/proc/driver/nvidia/gpus"))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.Equal(t, []string{"0000:07:00.0", "0000:0f:00.0"}, names,
		"one lowercased directory per GPU with an address")

	got, err := os.ReadFile(h.RootPath("driver/proc/driver/nvidia/gpus/0000:0f:00.0/information"))
	require.NoError(t, err)
	require.Equal(t, "Model: \t\t NVIDIA A100-SXM4-40GB\n"+
		"GPU UUID: \t GPU-bbbb\n"+
		"Bus Type: \t PCIe\n"+
		"Bus Location: \t 0000:0f:00.0\n"+
		"Device Minor: \t 3\n"+
		"GPU Excluded:\t No\n", string(got))
}

func TestWriteProcFS_PrunesRemovedGPUs(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)
	state.Devices = []agent.DeviceSpec{
		{Index: 0, PCIBusID: "0000:07:00.0"},
		{Index: 1, MinorNumber: 1, PCIBusID: "0000:0f:00.0"},
	}
	require.NoError(t, writeProcFS(t.Context(), h, state))

	state.Devices = state.Devices[:1]
	require.NoError(t, writeProcFS(t.Context(), h, state))

	entries, err := os.ReadDir(h.RootPath("driver/proc/driver/nvidia/gpus"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "0000:07:00.0", entries[0].Name())
}

// libmockfs serves /proc/driver/nvidia from <root>/proc/driver/nvidia, beside
// the /proc/modules it already serves from there.
func TestWriteProcFS_ServedWhereLibmockfsLooks(t *testing.T) {
	t.Parallel()

	h := testHost(t)
	state := testState(t)
	state.Devices = []agent.DeviceSpec{{Index: 0, PCIBusID: "0000:07:00.0", UUID: "GPU-aaaa"}}

	require.NoError(t, writeProcFS(t.Context(), h, state))
	require.NoError(t, writeProcFS(t.Context(), h, state), "re-staging replaces the link")

	got, err := os.ReadFile(h.RootPath("proc/driver/nvidia/gpus/0000:07:00.0/information"))
	require.NoError(t, err)
	require.Contains(t, string(got), "GPU-aaaa")

	require.NoError(t, New(h).Discard(t.Context()))
	_, err = os.Lstat(h.RootPath("proc/driver/nvidia"))
	require.True(t, os.IsNotExist(err), "Discard removes the link")
}

// ─── Apply / Revoke ──────────────────────────────────────────────────────────

// bindMountCleanup unmounts h's driver root at test end: Apply's mount is a
// real mount on the test machine, not sandboxed to h's t.TempDir().
func bindMountCleanup(t *testing.T, h *host.Host) {
	t.Helper()
	t.Cleanup(func() { _ = fsutil.Unmount(h.RootPath("driver"), h.RunPath("nvidia/driver")) })
}

// stageDriverTree stages the procfs entries and stands in for the userspace
// the nvml simulator stages in the same tree, which Apply publishes with them.
func stageDriverTree(t *testing.T, h *host.Host, state *agent.State) {
	t.Helper()
	require.NoError(t, writeProcFS(t.Context(), h, state))
	for _, rel := range []string{"driver/usr/bin/nvidia-smi", "driver/config/config.yaml"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(h.RootPath(rel)), 0o755))
		require.NoError(t, os.WriteFile(h.RootPath(rel), nil, 0o755))
	}
}

func TestApply_BindMountsTheDriverTreeOntoRunNvidiaDriver(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()
	bindMountCleanup(t, h)

	stageDriverTree(t, h, state)

	require.NoError(t, sim.Apply(ctx, state))
	require.True(t, sim.Ready())

	_, err := os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
	require.NoError(t, err, "nvidia-smi must be visible through the bind mount at /run/nvidia/driver")
	_, err = os.Stat(h.RunPath("nvidia/driver/proc/driver/nvidia/version"))
	require.NoError(t, err, "procfs must be visible through the bind mount")
	_, err = os.Stat(h.RunPath("nvidia/driver/config/config.yaml"))
	require.NoError(t, err, "engine config must be visible through the bind mount")
}

// Regression test: a consumer (e.g. the GPU Operator validator) can already
// have /run/nvidia/driver bind-mounted before Apply runs; replacing that
// directory entry would orphan its mount permanently.
func TestApply_DoesNotReplaceAnExistingDirectoryEntry(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()
	bindMountCleanup(t, h)

	stageDriverTree(t, h, state)

	preexisting := h.RunPath("nvidia/driver")
	require.NoError(t, os.MkdirAll(preexisting, 0o755))
	before, err := os.Stat(preexisting)
	require.NoError(t, err)

	require.NoError(t, sim.Apply(ctx, state))

	_, err = os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
	require.NoError(t, err, "content must be visible inside the pre-existing directory")

	// stat(preexisting) now resolves through the mount, to source's inode —
	// that's what a bind mount is. The invariant that actually matters is
	// that the pre-existing directory survives underneath it, unchanged:
	// unmount and confirm the same directory entry is still there.
	require.NoError(t, fsutil.Unmount(h.RootPath("driver"), preexisting))
	after, err := os.Stat(preexisting)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after),
		"Apply must not replace the /run/nvidia/driver directory entry")
}

func TestApply_IdempotentWhenAlreadyMounted(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()
	bindMountCleanup(t, h)

	stageDriverTree(t, h, state)

	require.NoError(t, sim.Apply(ctx, state))
	require.NoError(t, sim.Apply(ctx, state), "a second Apply must not error or stack another mount")
}

// Regression test: Apply must not mistake a mount from another owner for
// its own, or it would report Ready without ever publishing the driver tree.
func TestApply_RefusesAForeignMount(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()
	driverRoot := h.RunPath("nvidia/driver")

	foreignSrc := t.TempDir()
	require.NoError(t, fsutil.BindMount(foreignSrc, driverRoot))
	t.Cleanup(func() { _ = fsutil.Unmount(foreignSrc, driverRoot) })

	stageDriverTree(t, h, state)

	require.Error(t, sim.Apply(ctx, state))
	require.False(t, sim.Ready())
}

func TestRevoke_UnmountsButKeepsTheDirectory(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)
	ctx := t.Context()

	stageDriverTree(t, h, state)
	require.NoError(t, sim.Apply(ctx, state))

	require.NoError(t, sim.Revoke(ctx))

	_, err := os.Lstat(h.RunPath("nvidia/driver/usr/bin/nvidia-smi"))
	require.ErrorIs(t, err, os.ErrNotExist, "content must no longer be visible once unmounted")

	// The directory entry itself must survive: removing it would reintroduce
	// the same mount-orphaning risk Apply avoids.
	_, err = os.Stat(h.RunPath("nvidia/driver"))
	require.NoError(t, err, "the /run/nvidia/driver directory must survive Revoke")
}

func TestRevoke_IdempotentWhenNotMounted(t *testing.T) {
	skipUnlessRootLinux(t)

	sim := New(testHost(t))

	require.NoError(t, sim.Revoke(t.Context()), "Revoke with nothing mounted must not error")
}

// Revoke only ever unmounts; content it never mounted is left untouched.
func TestRevoke_LeavesForeignDirectoryContentAlone(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	path := h.RunPath("nvidia/driver")
	require.NoError(t, fsutil.Write(filepath.Join(path, "someone-elses-file"), []byte("driver"), 0o644))

	require.NoError(t, New(h).Revoke(t.Context()))

	_, err := os.Stat(filepath.Join(path, "someone-elses-file"))
	require.NoError(t, err, "Revoke must not touch content it never mounted")
}

// Regression test: Revoke must not detach a mount another owner put at
// /run/nvidia/driver.
func TestRevoke_LeavesAForeignMountAlone(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	driverRoot := h.RunPath("nvidia/driver")

	foreignSrc := t.TempDir()
	require.NoError(t, fsutil.BindMount(foreignSrc, driverRoot))
	t.Cleanup(func() { _ = fsutil.Unmount(foreignSrc, driverRoot) })

	require.NoError(t, New(h).Revoke(t.Context()))

	mounted, err := fsutil.IsMounted(driverRoot)
	require.NoError(t, err)
	require.True(t, mounted, "a foreign mount must survive Revoke")
}

// ─── Discard ─────────────────────────────────────────────────────────────────

func TestDiscard_NopWhenNotReady(t *testing.T) {
	sim := New(testHost(t))

	// ready is false by default — Discard must be a no-op.
	require.NoError(t, sim.Discard(t.Context()))
}

// ─── full Stage (Linux root required) ────────────────────────────────────────

func TestStage_WritesAllSurfaces(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)
	bindMountCleanup(t, h)

	require.NoError(t, sim.Stage(t.Context(), state))
	require.False(t, sim.Ready(), "Stage does not bind-mount the driver tree onto /run/nvidia/driver")
	require.NoError(t, sim.Apply(t.Context(), state))
	require.True(t, sim.Ready())

	// procfs, visible through the bind mount at /run/nvidia/driver
	_, err := os.Stat(h.RunPath("nvidia/driver/proc/driver/nvidia/version"))
	require.NoError(t, err)

	// module tree
	_, err = os.Stat(h.RootPath(kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
	require.NoError(t, err)
	_, err = os.Stat(h.RootPath(kmod.ProcModulesRelPath))
	require.NoError(t, err)
}

func TestStage_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)

	h := testHost(t)
	sim := New(h)
	state := testState(t)

	require.NoError(t, sim.Stage(t.Context(), state))
	require.NoError(t, sim.Stage(t.Context(), state), "second Stage must not error")
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
	require.Contains(t, string(procModules), "nvidia 62312448 4 nvidia_uvm,")

	refcnt, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
	require.NoError(t, err)
	require.Equal(t, "4\n", string(refcnt))

	version, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "version"))
	require.NoError(t, err)
	require.Equal(t, state.Software.DriverVersion+"\n", string(version))

	hostRefcnt, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, "xfs", "refcnt"))
	require.NoError(t, err)
	require.Equal(t, "2\n", string(hostRefcnt),
		"a host module must keep its own refcnt so lsmod columns stay correct")
}

// internal/kmod covers the gate itself. This covers the wiring: the surface
// must follow the node shape, not a constant.
func TestWriteKernelModules_ServesTheFabricModulesWhenTheShapeEnablesIB(t *testing.T) {
	h := testHost(t)
	withProcModules(t, hostProcModulesLine)

	state := testState(t)
	state.NodeShape.Network.IBEnabled = true
	require.NoError(t, writeKernelModules(t.Context(), h, state))

	for _, name := range []string{kmod.NVIDIAPeermem, kmod.MLX5Core} {
		require.DirExists(t, filepath.Join(h.Root, kmod.SysModuleRelPath, name),
			"%s must reach the served tree when the node shape enables InfiniBand", name)
	}

	refcnt, err := os.ReadFile(filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "refcnt"))
	require.NoError(t, err)
	require.Equal(t, "5\n", string(refcnt), "nvidia_peermem joins the holders under IB")

	require.FileExists(t,
		filepath.Join(h.Root, kmod.SysModuleRelPath, kmod.NVIDIA, "holders", kmod.NVIDIAPeermem))
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
