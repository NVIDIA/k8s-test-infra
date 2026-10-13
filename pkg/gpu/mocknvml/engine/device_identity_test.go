// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/hex"
	"strings"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

const (
	identityUUID0 = "GPU-4a8f2c1e-9b3d-4e5f-8a7b-0c1d2e3f4a5b"
	identityUUID1 = "GPU-5b9e3d2f-0c4e-4f6a-9b8c-1d2e3f4a5b6c"
	identityUUID2 = "GPU-6caf4e3a-1d5f-4a7b-8c9d-2e3f4a5b6c7d"
)

// identityEngine models a GB200 compute tray: GPUs 0 and 1 are one superchip
// and share its board serial, GPU 2 sits alone on its own.
func identityEngine(t *testing.T) *Engine {
	t.Helper()
	return pcieIdentityEngine(t, DeviceConfig{},
		DeviceOverride{Index: 0, UUID: identityUUID0, DeviceConfig: DeviceConfig{Serial: "1562849103700"}},
		DeviceOverride{Index: 1, UUID: identityUUID1, DeviceConfig: DeviceConfig{Serial: "1562849103700"}},
		DeviceOverride{Index: 2, UUID: identityUUID2, DeviceConfig: DeviceConfig{Serial: "1562849103701"}},
	)
}

func handleUUID(t *testing.T, e *Engine, handle unsafe.Pointer) string {
	t.Helper()
	dev := e.LookupConfigurableDevice(handle)
	require.NotNil(t, dev, "handle does not resolve to a device")
	uuid, ret := dev.GetUUID()
	require.Equal(t, nvml.SUCCESS, ret, "GetUUID")
	return uuid
}

func binaryUUID(t *testing.T, uuid string) [16]byte {
	t.Helper()
	raw, err := hex.DecodeString(strings.ReplaceAll(trimUUIDPrefix(uuid), "-", ""))
	require.NoError(t, err, "test UUID %q is not hex", uuid)
	require.Len(t, raw, 16, "test UUID %q", uuid)
	return [16]byte(raw)
}

func TestEngine_DeviceGetHandleBySerial(t *testing.T) {
	t.Parallel()
	e := identityEngine(t)

	handle, ret := e.DeviceGetHandleBySerial("1562849103701")
	require.Equal(t, nvml.SUCCESS, ret, "a serial on one board resolves")
	require.Equal(t, identityUUID2, handleUUID(t, e, handle))

	// The deprecation note in nvml.h: a serial names a board, and a dual-GPU
	// board cannot say which of its GPUs the caller meant.
	_, ret = e.DeviceGetHandleBySerial("1562849103700")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "a serial shared by two GPUs is ambiguous")

	_, ret = e.DeviceGetHandleBySerial("0000000000000")
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "unknown serial")

	_, ret = e.DeviceGetHandleBySerial("")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "an empty serial must not match a GPU with no serial")
}

func TestEngine_DeviceGetHandleBySerial_Uninitialized(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{NumDevices: 1, DriverVersion: "550.163.01"})

	_, ret := e.DeviceGetHandleBySerial("1562849103701")
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret)
}

// A GPU hidden by CUDA_VISIBLE_DEVICES-style filtering is not on the system as
// far as this process is concerned, so it neither resolves nor makes a shared
// serial ambiguous.
func TestEngine_DeviceGetHandleBySerial_HonoursVisibility(t *testing.T) {
	t.Parallel()
	e := identityEngine(t)
	e.server.setVisibleDevices([]int{1, 2})

	handle, ret := e.DeviceGetHandleBySerial("1562849103700")
	require.Equal(t, nvml.SUCCESS, ret, "only one visible GPU carries the serial")
	require.Equal(t, identityUUID1, handleUUID(t, e, handle))
}

func TestEngine_DeviceGetHandleByUUIDBinary(t *testing.T) {
	t.Parallel()
	e := identityEngine(t)

	handle, ret := e.DeviceGetHandleByUUIDBinary(binaryUUID(t, identityUUID1))
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, identityUUID1, handleUUID(t, e, handle))

	_, ret = e.DeviceGetHandleByUUIDBinary([16]byte{0xde, 0xad})
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// The binary form carries no case, so a profile spelling its UUID in upper case
// must still resolve.
func TestEngine_DeviceGetHandleByUUIDBinary_IgnoresConfiguredCase(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{},
		DeviceOverride{Index: 0, UUID: strings.ToUpper(identityUUID0)})

	_, ret := e.DeviceGetHandleByUUIDBinary(binaryUUID(t, identityUUID0))
	require.Equal(t, nvml.SUCCESS, ret)
}

// The binary form also drops the GPU-/MIG- prefix, so the same 16 bytes have
// to find a MIG device the way the ASCII lookup does.
func TestEngine_DeviceGetHandleByUUIDBinary_FindsMigDevice(t *testing.T) {
	t.Parallel()
	e := newFullyPartitionedEngine(t, 1)
	parent, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	migUUID := handleUUID(t, e, mustMigDeviceHandle(t, e, parent, 3))

	handle, ret := e.DeviceGetHandleByUUIDBinary(binaryUUID(t, migUUID))
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, migUUID, handleUUID(t, e, handle))
}

func TestConfigurableDevice_GetNumGpuCores(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{NumGPUCores: 16896}, DeviceOverride{Index: 0})

	cores, ret := pcieIdentityDevice(t, e, 0).GetNumGpuCores()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 16896, cores)
}

func TestConfigurableDevice_GetNumGpuCores_Unconfigured(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})

	_, ret := pcieIdentityDevice(t, e, 0).GetNumGpuCores()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "no core count rather than zero cores")
}

// nvml.h: core count is not supported on a MIG-enabled GPU or on a MIG device;
// the caller is pointed at the GPU instance profile instead.
func TestConfigurableDevice_GetNumGpuCores_MIG(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{
		NumDevices:    1,
		DriverVersion: "550.54.15",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			DeviceDefaults: DeviceConfig{
				Name:        "NVIDIA A100-SXM4-40GB",
				NumGPUCores: 6912,
				Memory:      &MemoryConfig{TotalBytes: a100_40GiB},
				MIG: &MIGConfig{
					ModeCurrent:       "enabled",
					ModePending:       "enabled",
					MaxGPUInstances:   7,
					SupportedProfiles: a100SupportedProfiles(),
					GPUInstances:      []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
				},
			},
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	parentHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	parent := e.LookupConfigurableDevice(parentHandle)

	_, ret = parent.GetNumGpuCores()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "MIG-enabled parent")

	mig := e.LookupConfigurableDevice(mustMigDeviceHandle(t, e, parentHandle, 0))
	_, ret = mig.GetNumGpuCores()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "MIG device")
}

func TestConfigurableDevice_GetAddressingMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		configured string
		want       uint32
		ret        nvml.Return
	}{
		{configured: "none", want: uint32(nvml.DEVICE_ADDRESSING_MODE_NONE), ret: nvml.SUCCESS},
		{configured: "hmm", want: uint32(nvml.DEVICE_ADDRESSING_MODE_HMM), ret: nvml.SUCCESS},
		{configured: " ATS ", want: uint32(nvml.DEVICE_ADDRESSING_MODE_ATS), ret: nvml.SUCCESS},
		// Undeclared or misspelt renders N/A, never a mode the host may not run.
		{configured: "", ret: nvml.ERROR_NOT_SUPPORTED},
		{configured: "uvm", ret: nvml.ERROR_NOT_SUPPORTED},
	} {
		t.Run(tc.configured, func(t *testing.T) {
			t.Parallel()
			e := pcieIdentityEngine(t, DeviceConfig{AddressingMode: tc.configured}, DeviceOverride{Index: 0})

			mode, ret := pcieIdentityDevice(t, e, 0).GetAddressingMode()
			require.Equal(t, tc.ret, ret)
			require.Equal(t, tc.want, mode.Value)
		})
	}
}

// No mocked board enables Extended GPU Memory, so the mask is empty: that is
// the "egm: disabled" every hardware capture shows, where NOT_SUPPORTED would
// render N/A.
func TestConfigurableDevice_GetCapabilities(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})

	caps, ret := pcieIdentityDevice(t, e, 0).GetCapabilities()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, caps.CapMask&nvml.DEV_CAP_EGM, "EGM must read as disabled")
}

// The PDI identifies the physical GPU, so it has to be stable across the
// independent processes that load the mock and distinct across GPUs.
func TestConfigurableDevice_GetPdi(t *testing.T) {
	t.Parallel()
	first := identityEngine(t)
	second := identityEngine(t)

	seen := map[uint64]int{}
	for i := range 3 {
		pdi, ret := pcieIdentityDevice(t, first, i).GetPdi()
		require.Equal(t, nvml.SUCCESS, ret, "GPU %d", i)
		require.NotZero(t, pdi.Value, "GPU %d", i)

		again, ret := pcieIdentityDevice(t, second, i).GetPdi()
		require.Equal(t, nvml.SUCCESS, ret, "GPU %d", i)
		require.Equal(t, pdi.Value, again.Value, "GPU %d PDI must not depend on the process", i)

		prev, dup := seen[pdi.Value]
		require.False(t, dup, "GPU %d duplicates the PDI of GPU %d", i, prev)
		seen[pdi.Value] = i
	}
}

func TestConfigurableDevice_GetPdi_MigDeviceReportsItsGPU(t *testing.T) {
	t.Parallel()
	e := newFullyPartitionedEngine(t, 1)
	parentHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	want, ret := e.LookupConfigurableDevice(parentHandle).GetPdi()
	require.Equal(t, nvml.SUCCESS, ret)
	got, ret := e.LookupConfigurableDevice(mustMigDeviceHandle(t, e, parentHandle, 2)).GetPdi()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, want.Value, got.Value)
}

// Every mocked GPU is its own board — GetMultiGpuBoard says so — so only a GPU
// and its own MIG devices share one. A GB200 superchip's two GPUs share a
// serial but are still two boards, as the hardware capture's distinct
// board_id values show.
func TestConfigurableDevice_OnSameBoard(t *testing.T) {
	t.Parallel()
	e := identityEngine(t)
	gpu0 := pcieIdentityDevice(t, e, 0)
	gpu1 := pcieIdentityDevice(t, e, 1)

	same, ret := gpu0.OnSameBoard(gpu0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 1, same, "a GPU is on its own board")

	same, ret = gpu0.OnSameBoard(gpu1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, same, "superchip siblings are separate boards")

	_, ret = gpu0.OnSameBoard(InvalidDeviceInstance)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

func TestConfigurableDevice_OnSameBoard_MigDevice(t *testing.T) {
	t.Parallel()
	e := newFullyPartitionedEngine(t, 1)
	parentHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	parent := e.LookupConfigurableDevice(parentHandle)
	mig := e.LookupConfigurableDevice(mustMigDeviceHandle(t, e, parentHandle, 0))

	same, ret := mig.OnSameBoard(parent)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 1, same)
}

func TestConfigurableDevice_GetBusType(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})

	busType, ret := pcieIdentityDevice(t, e, 0).GetBusType()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.BusType(nvml.BUS_TYPE_PCIE), busType)
}

// GetPcieSpeed reports the current link's per-lane rate in Mbps, while
// GetPcieLinkMaxSpeed answers with an NVML_PCIE_LINK_MAX_SPEED_* code for the
// fastest generation the link supports — a Gen5 board reports 32000 and 5.
func TestConfigurableDevice_PcieSpeeds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		pcie     *PCIeConfig
		speed    int
		speedRet nvml.Return
		maxSpeed uint32
		maxRet   nvml.Return
	}{
		{name: "gen5", pcie: &PCIeConfig{MaxLinkGen: 5, CurrentLinkGen: 5},
			speed: 32000, speedRet: nvml.SUCCESS, maxSpeed: nvml.PCIE_LINK_MAX_SPEED_32000MBPS, maxRet: nvml.SUCCESS},
		{name: "gen4 trained at gen1", pcie: &PCIeConfig{MaxLinkGen: 4, CurrentLinkGen: 1},
			speed: 2500, speedRet: nvml.SUCCESS, maxSpeed: nvml.PCIE_LINK_MAX_SPEED_16000MBPS, maxRet: nvml.SUCCESS},
		{name: "gen6", pcie: &PCIeConfig{MaxLinkGen: 6, CurrentLinkGen: 6},
			speed: 64000, speedRet: nvml.SUCCESS, maxSpeed: nvml.PCIE_LINK_MAX_SPEED_64000MBPS, maxRet: nvml.SUCCESS},
		{name: "unconfigured", pcie: nil,
			speedRet: nvml.ERROR_NOT_SUPPORTED, maxRet: nvml.ERROR_NOT_SUPPORTED},
		{name: "beyond gen6", pcie: &PCIeConfig{MaxLinkGen: 7, CurrentLinkGen: 7},
			speedRet: nvml.ERROR_NOT_SUPPORTED, maxRet: nvml.ERROR_NOT_SUPPORTED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dev := pcieIdentityDevice(t, pcieIdentityEngine(t, DeviceConfig{PCIe: tc.pcie}, DeviceOverride{Index: 0}), 0)

			speed, ret := dev.GetPcieSpeed()
			require.Equal(t, tc.speedRet, ret, "GetPcieSpeed")
			require.Equal(t, tc.speed, speed, "GetPcieSpeed")

			maxSpeed, ret := dev.GetPcieLinkMaxSpeed()
			require.Equal(t, tc.maxRet, ret, "GetPcieLinkMaxSpeed")
			require.Equal(t, tc.maxSpeed, maxSpeed, "GetPcieLinkMaxSpeed")
		})
	}
}

// Board features no mocked board has decline rather than invent a reading.
func TestConfigurableDevice_DeclinedIdentityQueries(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})
	dev := pcieIdentityDevice(t, e, 0)

	_, ret := dev.GetBridgeChipInfo()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetBridgeChipInfo: no multi-GPU board is mocked")

	_, ret = dev.GetIrqNum()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetIrqNum: no profile declares an interrupt line")
}

func TestConfigurableDevice_IdentityQueries_LostGPU(t *testing.T) {
	t.Parallel()
	defaults := withFailure(&FailureInjectionConfig{Mode: FailureModeLost})
	defaults.Serial = "1562849203750"
	e := NewEngine(&Config{
		NumDevices:    1,
		DriverVersion: "580.65.06",
		YAMLConfig: &YAMLConfig{
			Version:        "1.0",
			System:         SystemConfig{DriverVersion: "580.65.06", NumDevices: 1},
			DeviceDefaults: *defaults,
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	dev := e.LookupConfigurableDevice(handle)
	_, _ = dev.GetTemperature(nvml.TEMPERATURE_GPU) // trips the injector

	_, ret = dev.GetNumGpuCores()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetNumGpuCores")
	_, ret = dev.GetPdi()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetPdi")
	_, ret = dev.GetPcieSpeed()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetPcieSpeed")
	_, ret = dev.GetBBXTimeData_v1()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetBBXTimeData_v1")
	_, ret = e.DeviceGetHandleBySerial("1562849203750")
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "DeviceGetHandleBySerial")
}
