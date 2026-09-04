// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// driverDefaults is what the nvidia module reports when a profile declares no
// driver: block, and so what every shipped profile currently expects.
var driverDefaults = profile.DeviceFileParams{Mode: 0o666, ModifyDeviceFiles: true}

// stagedParams is a real params file, captured from an 8-GPU H100 node running
// the 580.105.08 open kernel module, which is also what the agent now renders.
// Held verbatim rather than built from the renderer: this fixture is the
// contract, so a change on either side that breaks the parse fails here.
//
// Note how little of it nvidia-modprobe can read. The scan ends at
// InitializeSystemMemoryAllocations on line 7, whose name is 33 characters and
// so wider than the field it is read into, stranding the 35 keys below — on
// real hardware exactly as here. That is why the four keys it consumes sit in
// lines 3 to 6, and why their position is the property worth testing.
const stagedParams = `ResmanDebugLevel: 4294967295
RmLogonRC: 1
ModifyDeviceFiles: 1
DeviceFileUID: 0
DeviceFileGID: 0
DeviceFileMode: 438
InitializeSystemMemoryAllocations: 1
UsePageAttributeTable: 4294967295
EnableMSI: 1
EnablePCIeGen3: 0
MemoryPoolSize: 0
KMallocHeapMaxSize: 0
VMallocHeapMaxSize: 0
IgnoreMMIOCheck: 0
EnableStreamMemOPs: 0
EnableUserNUMAManagement: 0
NvLinkDisable: 0
RmProfilingAdminOnly: 1
PreserveVideoMemoryAllocations: 1
EnableS0ixPowerManagement: 1
S0ixPowerManagementVideoMemoryThreshold: 256
DynamicPowerManagement: 3
DynamicPowerManagementVideoMemoryThreshold: 200
RegisterPCIDriver: 1
EnablePCIERelaxedOrderingMode: 0
EnableResizableBar: 0
EnableGpuFirmware: 18
EnableGpuFirmwareLogs: 2
RmNvlinkBandwidthLinkCount: 0
EnableDbgBreakpoint: 0
OpenRmEnableUnsupportedGpus: 1
DmaRemapPeerMmio: 1
ImexChannelCount: 2048
CreateImexChannel0: 0
GrdmaPciTopoCheckOverride: 1
CoherentGPUMemoryMode: "driver"
RegistryDwords: ""
RegistryDwordsPerDevice: ""
RmMsg: ""
GpuBlacklist: ""
TemporaryFilePath: "/var/tmp"
ExcludedGpus: ""
`

// brokenParams is the file the agent staged before this surface was fixed. It
// is the fixture that gives the check its value: every device-file key carries
// the NVreg_ prefix nvidia-modprobe does not match, and the empty-valued
// NVreg_RegistryDwords on line 2 ends the scan before any of them anyway.
const brokenParams = `EnableMSI: 1
NVreg_RegistryDwords:
NVreg_DeviceFileGID: 0
NVreg_DeviceFileMode: 438
NVreg_DeviceFileUID: 0
NVreg_ModifyDeviceFiles: 1
NVreg_PreserveVideoMemoryAllocations: 0
NVreg_EnableResizableBar: 0
`

func TestParamsProblems_StagedFileSatisfiesNvidiaModprobe(t *testing.T) {
	t.Parallel()

	require.Empty(t, ParamsProblems(stagedParams, driverDefaults))
}

// The pre-fix file must be rejected, and for both of its reasons: a check that
// passes on it would be asserting nothing.
func TestParamsProblems_RejectsThePreFixFile(t *testing.T) {
	t.Parallel()

	problems := ParamsProblems(brokenParams, driverDefaults)

	require.NotEmpty(t, problems, "the pre-fix params file must not satisfy the check")
	assert.Contains(t, problems, "ModifyDeviceFiles: nvidia-modprobe cannot reach this key")
	assert.Contains(t, problems, "DeviceFileMode: nvidia-modprobe cannot reach this key")
}

// A key sitting below the scan stopper is unreachable even though grep finds
// it, which is the failure mode the whole file ordering exists to avoid.
func TestParamsProblems_RejectsKeysBelowTheScanStopper(t *testing.T) {
	t.Parallel()

	// Correct names and values, but ordered so the quoted string param comes
	// first — the arrangement the original file had.
	stranded := "RegistryDwords: \"\"\nModifyDeviceFiles: 1\nDeviceFileUID: 0\nDeviceFileGID: 0\nDeviceFileMode: 438\n"

	problems := ParamsProblems(stranded, driverDefaults)

	require.Len(t, problems, 4, "all four consumed keys are below the stopper: %v", problems)
}

// An over-long name ends the scan the same way a non-integer value does, so a
// reordering that pushed the device-file keys below one has to fail too.
func TestParamsProblems_RejectsKeysBelowAnOverLongName(t *testing.T) {
	t.Parallel()

	// InitializeSystemMemoryAllocations is 33 characters, past the 31-char
	// field width, so nothing below it is reachable.
	stranded := "InitializeSystemMemoryAllocations: 1\nModifyDeviceFiles: 1\nDeviceFileUID: 0\nDeviceFileGID: 0\nDeviceFileMode: 438\n"

	problems := ParamsProblems(stranded, driverDefaults)

	require.Len(t, problems, 4, "all four consumed keys are below the over-long name: %v", problems)
}

// The point of fixing params at all: the device-file triple has to carry what
// the profile asked for, so a permission-failure scenario is reproducible.
func TestParamsProblems_ReportsValuesThatDepartFromTheProfile(t *testing.T) {
	t.Parallel()

	want := profile.DeviceFileParams{UID: 0, GID: 27, Mode: 0o660, ModifyDeviceFiles: true}

	problems := ParamsProblems(stagedParams, want)

	assert.Contains(t, problems, "DeviceFileGID: 0, want 27")
	assert.Contains(t, problems, "DeviceFileMode: 438, want 432")
}

func TestParamsProblems_ReportsModifyDeviceFilesDisabled(t *testing.T) {
	t.Parallel()

	want := profile.DeviceFileParams{Mode: 0o666, ModifyDeviceFiles: false}

	problems := ParamsProblems(stagedParams, want)

	assert.Contains(t, problems, "ModifyDeviceFiles: 1, want 0")
}

func TestParamsProblems_ReportsAnEmptyFile(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, ParamsProblems("", driverDefaults))
}
