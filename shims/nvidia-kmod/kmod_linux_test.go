//go:build integration

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvidiakmod_test drives the built modules against a real kernel.
//
// The assertions are deliberately made through the same interfaces the
// consumers use: unix.DeleteModule is the call k8s-driver-manager issues, and
// every file read here is one that the GPU Operator's driver container or
// operator-validator reads. Nothing is asserted through a Mokka-owned API,
// because the point of the module is that it satisfies code Mokka does not
// control.
//
// The tests do not run in parallel. Loaded modules are global kernel state with
// no namespace, so two tests loading nvidia at once would see each other's
// refcounts.
//
// The _linux filename suffix is load-bearing rather than decorative:
// unix.FinitModule and unix.DeleteModule exist only on Linux, so without it the
// package does not compile on a macOS workstation with the integration tag set.
package nvidiakmod_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// modules in load order. Unloading walks this in reverse, which is also the
// order k8s-driver-manager and the nvidia-driver entrypoint use.
var dependents = []string{"nvidia_modeset", "nvidia_uvm", "nvidia_peermem"}

// testGPUs is passed as the mokka_gpus parameter. Values contain no spaces:
// finit_module takes a space-separated parameter string, so a model name with a
// space would need quoting the kernel parser applies inconsistently.
const testGPUs = "0000:07:00.0|GPU-11111111-2222-3333-4444-555555555555|NVIDIA-A100-SXM4-80GB|0," +
	"0000:0b:00.0|GPU-66666666-7777-8888-9999-000000000000|NVIDIA-A100-SXM4-80GB|1"

// requireLoadable skips unless this host can actually load the built modules.
// Skipping rather than failing keeps the suite runnable on a CI runner whose
// headers package lagged its kernel — the tests then show up as skipped with a
// reason instead of disappearing.
func requireLoadable(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("requires root: loading a module needs CAP_SYS_MODULE")
	}
	if _, err := os.Stat(modulePath(t, "nvidia")); err != nil {
		t.Skipf("modules not built: %v (run make -C shims/nvidia-kmod)", err)
	}

	// A real driver must never be torn down by a test. These tests call
	// delete_module("nvidia"), which on a GPU node would unload the actual
	// driver out from under every running workload.
	if _, err := os.Stat("/sys/module/nvidia"); err == nil {
		t.Skip("/sys/module/nvidia already present: refusing to run against a loaded driver")
	}
}

// modulePath resolves a built module in dist/, which is where the Makefile
// points Kbuild's M so that no artifact lands beside the sources.
func modulePath(t *testing.T, name string) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	return filepath.Join(wd, "dist", name+".ko")
}

func insmod(t *testing.T, name, params string) error {
	t.Helper()

	f, err := os.Open(modulePath(t, name))
	require.NoError(t, err)
	defer f.Close() //nolint:errcheck // read-only fd, nothing to recover from

	return unix.FinitModule(int(f.Fd()), params, 0)
}

func rmmod(name string) error {
	return unix.DeleteModule(name, 0)
}

// loadFamily loads nvidia plus every dependent and registers the teardown that
// removes them, so a failing assertion cannot leave modules behind for the next
// test to trip over.
func loadFamily(t *testing.T, params string) {
	t.Helper()

	require.NoError(t, insmod(t, "nvidia", params))
	t.Cleanup(func() {
		for _, name := range dependents {
			_ = rmmod(name)
		}
		_ = rmmod("nvidia")
	})

	for _, name := range dependents {
		require.NoError(t, insmod(t, name, ""), "loading %s", name)
	}
}

func readSysModule(t *testing.T, module, attr string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("/sys/module", module, attr))
	require.NoError(t, err)

	return strings.TrimSpace(string(b))
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(b)
}

// procModulesEntry returns the /proc/modules fields for a module: name, size,
// refcount, used-by list, state, address.
func procModulesEntry(t *testing.T, module string) []string {
	t.Helper()

	for _, line := range strings.Split(readFile(t, "/proc/modules"), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[0] == module {
			return fields
		}
	}

	t.Fatalf("no /proc/modules entry for %s", module)

	return nil
}

// TestPresence covers the surface k8s-driver-manager's isDriverLoaded and the
// driver container's _should_skip_kernel_module_reload gate on. Both test for
// /sys/module/nvidia/refcnt and do nothing further when it is absent.
func TestPresence(t *testing.T) {
	requireLoadable(t)

	require.NoError(t, insmod(t, "nvidia", ""))
	t.Cleanup(func() { _ = rmmod("nvidia") })

	require.Equal(t, "live", readSysModule(t, "nvidia", "initstate"))
	require.Equal(t, "0", readSysModule(t, "nvidia", "refcnt"),
		"nothing holds the module yet")
	require.NotEmpty(t, readSysModule(t, "nvidia", "version"))

	entry := procModulesEntry(t, "nvidia")
	require.Equal(t, "0", entry[2], "refcount column")
	require.Equal(t, "-", entry[3], "used-by column with no dependents")
	require.Equal(t, "Live", entry[4])
}

// TestModuleParameters checks the NVreg_* surface against the values
// writeProcFS stages in internal/agent/gpudriver/stage.go. The two render the
// same settings and must not disagree.
func TestModuleParameters(t *testing.T) {
	requireLoadable(t)

	require.NoError(t, insmod(t, "nvidia", ""))
	t.Cleanup(func() { _ = rmmod("nvidia") })

	for attr, want := range map[string]string{
		"NVreg_EnableMSI":                      "1",
		"NVreg_DeviceFileGID":                  "0",
		"NVreg_DeviceFileMode":                 "438",
		"NVreg_DeviceFileUID":                  "0",
		"NVreg_ModifyDeviceFiles":              "1",
		"NVreg_PreserveVideoMemoryAllocations": "0",
		"NVreg_EnableResizableBar":             "0",
	} {
		require.Equal(t, want, readSysModule(t, "nvidia", "parameters/"+attr), attr)
	}
}

// TestCharDeviceMajors is the one assertion with a consequence today: Mokka
// mknods /dev/nvidia* with these majors but nothing had registered them, so
// open() returned ENXIO and every consumer that probes a GPU by opening its
// device silently failed.
func TestCharDeviceMajors(t *testing.T) {
	requireLoadable(t)

	loadFamily(t, "")

	devices := readFile(t, "/proc/devices")
	require.Contains(t, devices, "195 nvidia",
		"the major charDevsForDevices mknods for per-GPU nodes and nvidiactl")
	require.Contains(t, devices, "510 nvidia-uvm",
		"where nvidia-container-cli looks up the uvm major")

	// Created under /dev rather than a temp dir: a tmpfs mounted nodev would
	// refuse to treat the node as a device and the test would prove nothing.
	node := "/dev/mokka-kmod-test-nvidia0"
	require.NoError(t, unix.Mknod(node, unix.S_IFCHR|0o600, int(unix.Mkdev(195, 0))))
	t.Cleanup(func() { _ = os.Remove(node) })

	f, err := os.OpenFile(node, os.O_RDWR, 0)
	require.NoError(t, err, "open must succeed once major 195 is registered")
	require.NoError(t, f.Close())
}

// TestRefcountAndHolders is the core of the prototype. Every property here is
// produced by the kernel because the dependents reference an exported symbol —
// none of it is implemented in the module.
func TestRefcountAndHolders(t *testing.T) {
	requireLoadable(t)

	loadFamily(t, "")

	require.Equal(t, strconv.Itoa(len(dependents)), readSysModule(t, "nvidia", "refcnt"))

	holders, err := os.ReadDir("/sys/module/nvidia/holders")
	require.NoError(t, err)

	names := make([]string, 0, len(holders))
	for _, h := range holders {
		names = append(names, h.Name())
	}
	require.ElementsMatch(t, dependents, names)

	entry := procModulesEntry(t, "nvidia")
	require.Equal(t, strconv.Itoa(len(dependents)), entry[2])
	for _, name := range dependents {
		require.Contains(t, entry[3], name, "used-by column, as lsmod renders it")
	}
}

// TestDeleteModuleInUse is what no rendered file tree can imitate: the kernel
// refuses the syscall while a dependent is loaded. k8s-driver-manager makes
// exactly this call, and its unload ordering exists because of this error.
//
// The error is EWOULDBLOCK (== EAGAIN), which is what try_stop_module() returns
// when the refcount is non-zero. EBUSY is a different case -- a module with an
// init but no exit, which can never be unloaded at all -- so code testing for
// EBUSY to detect "driver in use" would never match.
func TestDeleteModuleInUse(t *testing.T) {
	requireLoadable(t)

	loadFamily(t, "")

	require.ErrorIs(t, rmmod("nvidia"), unix.EWOULDBLOCK,
		"nvidia must not unload while nvidia_uvm and friends hold it")

	// The documented teardown order, dependents first.
	for _, name := range dependents {
		require.NoError(t, rmmod(name), "unloading %s", name)
	}
	require.NoError(t, rmmod("nvidia"))

	_, err := os.Stat("/sys/module/nvidia")
	require.True(t, errors.Is(err, os.ErrNotExist), "module tree must be gone: %v", err)
}

// TestProcDriverNvidia checks the text the module serves against what
// gpudriver.writeProcFS stages. Both can exist at once, so they have to agree.
func TestProcDriverNvidia(t *testing.T) {
	requireLoadable(t)

	loadFamily(t, "mokka_gpus="+testGPUs)

	version := readFile(t, "/proc/driver/nvidia/version")
	require.True(t, strings.HasPrefix(version, "NVRM version: NVIDIA UNIX "), version)
	require.Contains(t, version, " Kernel Module  "+readSysModule(t, "nvidia", "version")+"  ",
		"the procfs banner and /sys/module/nvidia/version must name one driver")
	require.Contains(t, version, "GCC version:  ")

	// Byte for byte, in writeProcFS's order, including the bare
	// NVreg_RegistryDwords line with nothing after the colon.
	require.Equal(t, "EnableMSI: 1\n"+
		"NVreg_RegistryDwords:\n"+
		"NVreg_DeviceFileGID: 0\n"+
		"NVreg_DeviceFileMode: 438\n"+
		"NVreg_DeviceFileUID: 0\n"+
		"NVreg_ModifyDeviceFiles: 1\n"+
		"NVreg_PreserveVideoMemoryAllocations: 0\n"+
		"NVreg_EnableResizableBar: 0\n",
		readFile(t, "/proc/driver/nvidia/params"))

	info := readFile(t, "/proc/driver/nvidia/gpus/0000:0b:00.0/information")
	require.Contains(t, info, "GPU UUID: \t GPU-66666666-7777-8888-9999-000000000000")
	require.Contains(t, info, "Bus Location: \t 0000:0b:00.0")
	require.Contains(t, info, "Device Minor: \t 1")
	require.Contains(t, info, "Model: \t\t NVIDIA-A100-SXM4-80GB")
}

// TestProcfsRemovedOnUnload guards the half of the lifecycle that a staged file
// tree gets wrong: files written to disk outlive the driver, while the real
// procfs entries disappear with the module.
func TestProcfsRemovedOnUnload(t *testing.T) {
	requireLoadable(t)

	require.NoError(t, insmod(t, "nvidia", "mokka_gpus="+testGPUs))
	t.Cleanup(func() { _ = rmmod("nvidia") })

	require.FileExists(t, "/proc/driver/nvidia/gpus/0000:07:00.0/information")
	require.NoError(t, rmmod("nvidia"))

	_, err := os.Stat("/proc/driver/nvidia")
	require.True(t, errors.Is(err, os.ErrNotExist), "procfs tree must be gone: %v", err)
}
