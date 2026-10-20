//go:build integration

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvidiakmod_test drives the built modules against a real kernel.
//
// The assertions are deliberately made through the same interfaces the
// consumers use: unix.DeleteModule is the call k8s-driver-manager issues, the
// ioctls are the ones NVIDIA's userspace components issue, and every file read
// here is one that the GPU Operator's driver container or operator-validator
// reads. Nothing is asserted through a Mokka-owned API, because the point of
// the module is that it satisfies code Mokka does not control.
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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	nvidiakmod "github.com/NVIDIA/k8s-test-infra/shims/nvidia-kmod"
)

// modules in load order. Unloading walks this in reverse, which is also the
// order k8s-driver-manager and the nvidia-driver entrypoint use.
var dependents = []string{"nvidia_modeset", "nvidia_uvm", "nvidia_peermem"}

// testGPUs is a mokka_gpus value. The model names carry a space, as real ones
// do; the last field is the PCI device ID.
const testGPUs = "0000:07:00.0|GPU-11111111-2222-3333-4444-555555555555|NVIDIA A100-SXM4-80GB|0|20b2," +
	"0000:0b:00.0|GPU-66666666-7777-8888-9999-000000000000|NVIDIA A100-SXM4-80GB|1|20b2"

// gpusParam passes testGPUs quoted, which the kernel's parameter parser needs
// to keep the spaces inside the value.
const gpusParam = `mokka_gpus="` + testGPUs + `"`

// The device numbers and ioctl escapes, from nvidia_internal.h and
// nv_ioctl_abi.h.
const (
	nvMajor        = 195
	nvMinorControl = 255

	escCardInfo         = 200
	escCheckVersionStr  = 210
	escIoctlXferCmd     = 211
	escAttachGPUsToFD   = 212
	escNUMAInfo         = 215
	escWaitOpenComplete = 218

	// escRMControl is an RM escape, which the module deliberately does not
	// simulate.
	escRMControl = 0x2a

	versionCmdRelaxed  = '1'
	versionCmdQuery    = '2'
	numaStatusDisabled = 0
)

// The ioctl argument layouts, field for field as in nv_ioctl_abi.h, with the
// padding the C compiler inserts made explicit.
type rmAPIVersion struct {
	Cmd           uint32
	Reply         uint32
	VersionString [64]byte
}

type pciInfo struct {
	Domain   uint32
	Bus      uint8
	Slot     uint8
	Function uint8
	_        uint8
	VendorID uint16
	DeviceID uint16
}

type cardInfo struct {
	Valid         uint8
	_             [3]byte
	PCI           pciInfo
	GPUID         uint32
	InterruptLine uint16
	_             [2]byte
	RegAddress    uint64
	RegSize       uint64
	FBAddress     uint64
	FBSize        uint64
	MinorNumber   uint32
	DevName       [10]byte
	_             [2]byte
}

type waitOpenComplete struct {
	RC            int32
	AdapterStatus uint32
}

type numaInfo struct {
	NID              int32
	Status           int32
	MemblockSize     uint64
	NUMAMemAddr      uint64
	NUMAMemSize      uint64
	UseAutoOnline    uint8
	_                [7]byte
	OfflineAddresses [64]uint64
	NumEntries       uint32
	_                [4]byte
}

type ioctlXfer struct {
	Cmd  uint32
	Size uint32
	Ptr  uint64
}

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

// loadNvidia loads nvidia alone and registers its removal.
func loadNvidia(t *testing.T, params string) {
	t.Helper()

	require.NoError(t, insmod(t, "nvidia", params))
	t.Cleanup(func() { _ = rmmod("nvidia") })
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

// nvidiaNode mknods a node for (195, minor) and removes it when the test ends.
// Created under /dev rather than a temp dir: a tmpfs mounted nodev would refuse
// to treat the node as a device and the test would prove nothing.
func nvidiaNode(t *testing.T, minor uint32) string {
	t.Helper()

	node := fmt.Sprintf("/dev/mokka-kmod-test-nvidia%d", minor)
	_ = os.Remove(node) // left behind by an interrupted run; mknod refuses to replace it
	require.NoError(t, unix.Mknod(node, unix.S_IFCHR|0o600, int(unix.Mkdev(nvMajor, minor))))
	t.Cleanup(func() { _ = os.Remove(node) })

	return node
}

// openNode opens (195, minor) and closes it when the test ends, which is
// before any rmmod registered earlier: an open node holds the module.
func openNode(t *testing.T, minor uint32) *os.File {
	t.Helper()

	f, err := os.OpenFile(nvidiaNode(t, minor), os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	return f
}

// nvIoctl issues _IOWR('F', escape, size), the encoding every NVIDIA userspace
// component uses, with arg pointing at a value of that size.
func nvIoctl(f *os.File, escape uintptr, arg unsafe.Pointer, size uintptr) error {
	const iocReadWrite = 3

	req := iocReadWrite<<30 | size<<16 | uintptr('F')<<8 | escape

	// The uintptr conversion stays inside the call expression, the one place
	// the unsafe rules keep arg's referent alive for the length of the call.
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), req, uintptr(arg))
	if errno != 0 {
		return errno
	}

	return nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}

	return string(b)
}

// TestArgumentLayouts guards this file's copies of the ioctl arguments. The
// sizes are the ones measured from NVIDIA's upstream headers, which
// nv_ioctl_abi.h asserts too; a wrong copy here would test the module against
// a layout no real client uses.
func TestArgumentLayouts(t *testing.T) {
	t.Parallel()

	require.EqualValues(t, 72, unsafe.Sizeof(rmAPIVersion{}))
	require.EqualValues(t, 12, unsafe.Sizeof(pciInfo{}))
	require.EqualValues(t, 72, unsafe.Sizeof(cardInfo{}))
	require.EqualValues(t, 56, unsafe.Offsetof(cardInfo{}.MinorNumber))
	require.EqualValues(t, 8, unsafe.Sizeof(waitOpenComplete{}))
	require.EqualValues(t, 560, unsafe.Sizeof(numaInfo{}))
	require.EqualValues(t, 40, unsafe.Offsetof(numaInfo{}.OfflineAddresses))
	require.EqualValues(t, 16, unsafe.Sizeof(ioctlXfer{}))
}

// TestPresence covers the surface k8s-driver-manager's isDriverLoaded and the
// driver container's _should_skip_kernel_module_reload gate on. Both test for
// /sys/module/nvidia/refcnt and do nothing further when it is absent.
func TestPresence(t *testing.T) {
	requireLoadable(t)

	loadNvidia(t, "")

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

	loadNvidia(t, "")

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

// TestInvalidGPURecord: a record whose PCI location cannot be parsed fails the
// load instead of producing a GPU no client can find.
func TestInvalidGPURecord(t *testing.T) {
	requireLoadable(t)

	err := insmod(t, "nvidia", `mokka_gpus="07:00.0|GPU-11111111-2222-3333-4444-555555555555|NVIDIA A100-SXM4-80GB|0"`)
	if err == nil {
		_ = rmmod("nvidia")
	}
	require.ErrorIs(t, err, unix.EINVAL)

	_, err = os.Stat("/sys/module/nvidia")
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestCharDeviceMajors: Mokka mknods /dev/nvidia* with these majors, and a node
// whose major nothing registered cannot be opened at all, so every consumer
// that probes a GPU by opening its device fails without them.
func TestCharDeviceMajors(t *testing.T) {
	requireLoadable(t)

	loadFamily(t, gpusParam)

	devices := readFile(t, "/proc/devices")
	require.Contains(t, devices, "195 nvidia",
		"the major charDevsForDevices mknods for per-GPU nodes and nvidiactl")
	require.Contains(t, devices, "510 nvidia-uvm",
		"where nvidia-container-cli looks up the uvm major")

	openNode(t, 0)
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
	require.ErrorIs(t, err, os.ErrNotExist, "module tree must be gone")
}

// TestProcDriverNvidia checks the text the module serves against what
// gpudriver.writeProcFS stages. Both can exist at once, so they have to agree.
func TestProcDriverNvidia(t *testing.T) {
	requireLoadable(t)

	loadFamily(t, gpusParam)

	version := readFile(t, "/proc/driver/nvidia/version")
	require.True(t, strings.HasPrefix(version, "NVRM version: NVIDIA UNIX "), version)
	require.Contains(t, version, " Kernel Module  "+readSysModule(t, "nvidia", "version")+"  ",
		"the procfs banner and /sys/module/nvidia/version must name one driver")
	require.Contains(t, version, "GCC version:  ")

	// Byte for byte, in writeProcFS's order and spelling: the bare key names
	// the device-node code looks up, not the NVreg_ parameter names.
	require.Equal(t, "ModifyDeviceFiles: 1\n"+
		"DeviceFileUID: 0\n"+
		"DeviceFileGID: 0\n"+
		"DeviceFileMode: 438\n"+
		"EnableMSI: 1\n"+
		"PreserveVideoMemoryAllocations: 0\n"+
		"EnableResizableBar: 0\n"+
		"RegistryDwords: \"\"\n",
		readFile(t, "/proc/driver/nvidia/params"))

	require.Equal(t, "Model: \t\t NVIDIA A100-SXM4-80GB\n"+
		"GPU UUID: \t GPU-66666666-7777-8888-9999-000000000000\n"+
		"Bus Type: \t PCIe\n"+
		"Bus Location: \t 0000:0b:00.0\n"+
		"Device Minor: \t 1\n"+
		"GPU Excluded:\t No\n",
		readFile(t, "/proc/driver/nvidia/gpus/0000:0b:00.0/information"))
}

// TestProcfsRemovedOnUnload guards the half of the lifecycle that a staged file
// tree gets wrong: files written to disk outlive the driver, while the real
// procfs entries disappear with the module.
func TestProcfsRemovedOnUnload(t *testing.T) {
	requireLoadable(t)

	loadNvidia(t, gpusParam)

	require.FileExists(t, "/proc/driver/nvidia/gpus/0000:07:00.0/information")
	require.NoError(t, rmmod("nvidia"))

	_, err := os.Stat("/proc/driver/nvidia")
	require.ErrorIs(t, err, os.ErrNotExist, "procfs tree must be gone")
}

// TestVersionHandshake covers the first ioctl every driver client issues. A
// client of another release must be refused and told the module's version,
// since that is the message it reports to the user.
func TestVersionHandshake(t *testing.T) {
	requireLoadable(t)

	loadNvidia(t, "")

	version := readSysModule(t, "nvidia", "version")
	ctl := openNode(t, nvMinorControl)

	var exact rmAPIVersion
	copy(exact.VersionString[:], version)
	require.NoError(t, nvIoctl(ctl, escCheckVersionStr, unsafe.Pointer(&exact), unsafe.Sizeof(exact)))
	require.EqualValues(t, 1, exact.Reply, "recognized")

	var other rmAPIVersion
	copy(other.VersionString[:], "1.2.3")
	require.ErrorIs(t, nvIoctl(ctl, escCheckVersionStr, unsafe.Pointer(&other), unsafe.Sizeof(other)), unix.EINVAL)
	require.Equal(t, version, cString(other.VersionString[:]))

	// Relaxed matching compares up to the first dot: the branch.
	branch, _, _ := strings.Cut(version, ".")
	relaxed := rmAPIVersion{Cmd: versionCmdRelaxed}
	copy(relaxed.VersionString[:], branch+".0.0")
	require.NoError(t, nvIoctl(ctl, escCheckVersionStr, unsafe.Pointer(&relaxed), unsafe.Sizeof(relaxed)))

	query := rmAPIVersion{Cmd: versionCmdQuery}
	require.NoError(t, nvIoctl(ctl, escCheckVersionStr, unsafe.Pointer(&query), unsafe.Sizeof(query)))
	require.Equal(t, version, cString(query.VersionString[:]))
}

// TestCardInfo is what libnvidia-cfg lists GPUs with, and so what
// nvidia-persistenced refuses to start without.
func TestCardInfo(t *testing.T) {
	requireLoadable(t)

	loadNvidia(t, gpusParam)

	ctl := openNode(t, nvMinorControl)

	// Clients size the array for the most GPUs they support, not the GPUs present.
	var cards [32]cardInfo
	require.NoError(t, nvIoctl(ctl, escCardInfo, unsafe.Pointer(&cards), unsafe.Sizeof(cards)))

	require.EqualValues(t, 1, cards[1].Valid)
	require.Equal(t, pciInfo{Bus: 0x0b, VendorID: 0x10de, DeviceID: 0x20b2}, cards[1].PCI)
	require.EqualValues(t, 1, cards[1].MinorNumber)
	require.EqualValues(t, 0, cards[2].Valid, "the list ends after the last GPU")

	// Clients hand the GPU IDs back to attach to the GPUs.
	ids := [2]uint32{cards[0].GPUID, cards[1].GPUID}
	require.NoError(t, nvIoctl(ctl, escAttachGPUsToFD, unsafe.Pointer(&ids), unsafe.Sizeof(ids)))
	unknown := uint32(0xdead)
	require.ErrorIs(t, nvIoctl(ctl, escAttachGPUsToFD, unsafe.Pointer(&unknown), unsafe.Sizeof(unknown)), unix.EINVAL)

	// Too few entries for the GPUs present, and a size that is not a whole
	// number of entries, are refused as upstream refuses them.
	var one [1]cardInfo
	require.ErrorIs(t, nvIoctl(ctl, escCardInfo, unsafe.Pointer(&one), unsafe.Sizeof(one)), unix.EINVAL)
	var ragged [100]byte
	require.ErrorIs(t, nvIoctl(ctl, escCardInfo, unsafe.Pointer(&ragged), unsafe.Sizeof(ragged)), unix.EINVAL)

	// The same request through XFER, the indirection for arguments too large
	// for the size field of an ioctl command. The kernel dereferences Ptr
	// during the call, so the array must not move until it returns.
	var viaXfer [32]cardInfo
	var pinner runtime.Pinner
	pinner.Pin(&viaXfer)
	defer pinner.Unpin()

	xfer := ioctlXfer{
		Cmd:  escCardInfo,
		Size: uint32(unsafe.Sizeof(viaXfer)),
		Ptr:  uint64(uintptr(unsafe.Pointer(&viaXfer))),
	}
	require.NoError(t, nvIoctl(ctl, escIoctlXferCmd, unsafe.Pointer(&xfer), unsafe.Sizeof(xfer)))
	require.Equal(t, cards, viaXfer)
}

// TestGPUNodes covers what a client does with a GPU it has found:
// nvidia-persistenced opens the node, waits for the open to complete, and asks
// whether there is NUMA memory to online.
func TestGPUNodes(t *testing.T) {
	requireLoadable(t)

	loadNvidia(t, gpusParam)

	_, err := os.OpenFile(nvidiaNode(t, 7), os.O_RDWR, 0)
	require.ErrorIs(t, err, unix.ENODEV, "no GPU has minor 7")

	gpu := openNode(t, 1)

	wait := waitOpenComplete{RC: -1, AdapterStatus: 0xff}
	require.NoError(t, nvIoctl(gpu, escWaitOpenComplete, unsafe.Pointer(&wait), unsafe.Sizeof(wait)))
	require.Equal(t, waitOpenComplete{}, wait, "a completed open, adapter status NV_OK")

	var numa numaInfo
	require.NoError(t, nvIoctl(gpu, escNUMAInfo, unsafe.Pointer(&numa), unsafe.Sizeof(numa)))
	require.EqualValues(t, -1, numa.NID)
	require.EqualValues(t, numaStatusDisabled, numa.Status)

	// Each escape belongs to one kind of node, as upstream scopes them.
	ctl := openNode(t, nvMinorControl)
	var cards [32]cardInfo
	require.ErrorIs(t, nvIoctl(gpu, escCardInfo, unsafe.Pointer(&cards), unsafe.Sizeof(cards)), unix.EINVAL,
		"card info is answered on nvidiactl only")
	require.ErrorIs(t, nvIoctl(ctl, escNUMAInfo, unsafe.Pointer(&numa), unsafe.Sizeof(numa)), unix.EINVAL,
		"NUMA info is answered on GPU nodes only")
}

// TestUnsimulatedEscape: everything past the interface layer fails with
// ENOTTY rather than pretending to work.
func TestUnsimulatedEscape(t *testing.T) {
	requireLoadable(t)

	loadNvidia(t, "")

	ctl := openNode(t, nvMinorControl)

	var params [32]byte
	require.ErrorIs(t, nvIoctl(ctl, escRMControl, unsafe.Pointer(&params), unsafe.Sizeof(params)), unix.ENOTTY)
}

// TestLoader drives the loader the way the GPU Operator's driver container
// does: plain modprobe, with the options the container writes to
// /etc/modprobe.d before every load. The install directives must hand both
// those options and MOKKA_GPUS through to the simulated modules.
func TestLoader(t *testing.T) {
	requireLoadable(t)

	modprobe, err := exec.LookPath("modprobe")
	if err != nil {
		t.Skip("modprobe not found")
	}
	// The loader takes the version of an installed real module over
	// DRIVER_VERSION, which would make the version assertion host-dependent.
	if exec.Command("modinfo", "nvidia").Run() == nil {
		t.Skip("an NVIDIA kernel module is installed on this host")
	}

	wd, err := os.Getwd()
	require.NoError(t, err)

	// The directives the node agent stages, pointed at the checkout instead of
	// the driver container's mount.
	conf := nvidiakmod.ModprobeConf(wd) + "options nvidia NVreg_CoherentGPUMemoryMode=driver\n"
	confPath := filepath.Join(t.TempDir(), "mokka-nvidia.conf")
	require.NoError(t, os.WriteFile(confPath, []byte(conf), 0o600))

	t.Cleanup(func() {
		for _, name := range []string{"nvidia_modeset", "nvidia_uvm", "nvidia"} {
			_ = rmmod(name)
		}
	})

	const version = "580.126.20"
	buildDir := t.TempDir()
	for _, name := range []string{"nvidia", "nvidia-uvm", "nvidia-modeset"} {
		cmd := exec.Command(modprobe, "-C", confPath, name)
		cmd.Env = append(os.Environ(),
			"DRIVER_VERSION="+version, "MOKKA_GPUS="+testGPUs, "MOKKA_KMOD_BUILD_DIR="+buildDir)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "modprobe %s: %s", name, out)
	}

	require.Equal(t, version, readSysModule(t, "nvidia", "version"))
	require.Equal(t, "driver", readSysModule(t, "nvidia", "parameters/NVreg_CoherentGPUMemoryMode"))
	require.Equal(t, "2", readSysModule(t, "nvidia", "refcnt"), "nvidia_uvm and nvidia_modeset hold it")
	require.Contains(t, readFile(t, "/proc/driver/nvidia/gpus/0000:07:00.0/information"),
		"Model: \t\t NVIDIA A100-SXM4-80GB\n")
}
