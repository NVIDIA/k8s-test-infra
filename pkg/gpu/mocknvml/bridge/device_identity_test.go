// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// Callers allocate these from go-nvml's structs and stamp a version whose low
// 24 bits are that struct's size, so a C layout that drifts from go-nvml makes
// the bridge write past the caller's buffer or reject every version it sees.
func TestDeviceIdentityLayouts_MatchGoNvml(t *testing.T) {
	t.Parallel()
	var uuid nvml.UUID
	var pdi nvml.Pdi
	for _, tc := range []struct {
		name   string
		cSize, goSize uintptr
		want   uintptr
	}{
		{"UUID", uuidSizeForTest(), unsafe.Sizeof(uuid), 52},
		{"UUID.value offset", uuidValueOffsetForTest(), unsafe.Offsetof(uuid.Value), 8},
		{"Pdi", pdiSizeForTest(), unsafe.Sizeof(pdi), 16},
		{"Pdi.value offset", pdiValueOffsetForTest(), unsafe.Offsetof(pdi.Value), 8},
		{"DeviceAddressingMode", addressingModeSizeForTest(), unsafe.Sizeof(nvml.DeviceAddressingMode{}), 8},
		{"DeviceCapabilities", capabilitiesSizeForTest(), unsafe.Sizeof(nvml.DeviceCapabilities{}), 8},
		{"ClkMonStatus", clkMonStatusSizeForTest(), unsafe.Sizeof(nvml.ClkMonStatus{}), 264},
		{"BusType", busTypeSizeForTest(), unsafe.Sizeof(nvml.BusType(0)), 4},
	} {
		require.Equal(t, tc.want, tc.cSize, "%s: C layout", tc.name)
		require.Equal(t, tc.goSize, tc.cSize, "%s: C layout differs from go-nvml", tc.name)
	}
}

// gb200BridgeEngine loads the shipped GB200 profile: its 580 driver exposes
// the versioned identity calls, and its superchips share board serials.
func gb200BridgeEngine(t *testing.T) *engine.Engine {
	t.Helper()
	t.Setenv("MOCK_NVML_CONFIG", filepath.Join("..", "configs", "mock-nvml-config-gb200.yaml"))
	t.Setenv("MOCK_NVML_OVERRIDES", filepath.Join(t.TempDir(), "overrides.yaml"))
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)
	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	e.SetVisibleDevicesForTesting(nil)
	return e
}

func bridgeDeviceUUID(t *testing.T, e *engine.Engine, handle unsafe.Pointer) string {
	t.Helper()
	dev := e.LookupConfigurableDevice(handle)
	require.NotNil(t, dev, "handle does not resolve")
	uuid, ret := dev.GetUUID()
	require.Equal(t, nvml.SUCCESS, ret)
	return uuid
}

func TestDeviceGetHandleByUUIDV(t *testing.T) {
	e := gb200BridgeEngine(t)
	handle, ret := e.DeviceGetHandleByIndex(1)
	require.Equal(t, nvml.SUCCESS, ret)
	want := bridgeDeviceUUID(t, e, handle)
	binary, err := hex.DecodeString(strings.ReplaceAll(strings.TrimPrefix(want, "GPU-"), "-", ""))
	require.NoError(t, err)
	v1 := nvml.STRUCT_VERSION(nvml.UUID{}, 1)

	got, ret32 := handleByUUIDVForTest(v1, uint32(nvml.UUID_TYPE_ASCII), []byte(want))
	require.Equal(t, uint32(nvml.SUCCESS), ret32, "ASCII")
	require.Equal(t, want, bridgeDeviceUUID(t, e, got), "ASCII")

	got, ret32 = handleByUUIDVForTest(v1, uint32(nvml.UUID_TYPE_BINARY), binary)
	require.Equal(t, uint32(nvml.SUCCESS), ret32, "binary")
	require.Equal(t, want, bridgeDeviceUUID(t, e, got), "binary")

	_, ret32 = handleByUUIDVForTest(v1, uint32(nvml.UUID_TYPE_NONE), []byte(want))
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), ret32, "untyped UUID")

	_, ret32 = handleByUUIDVForTest(0, uint32(nvml.UUID_TYPE_ASCII), []byte(want))
	require.Equal(t, uint32(nvml.ERROR_ARGUMENT_VERSION_MISMATCH), ret32, "unstamped version")
}

// Every serial on a GB200 tray is shared by the two GPUs of a superchip, so
// the deprecated lookup can never resolve one there.
func TestDeviceGetHandleBySerial_SharedSuperchipSerial(t *testing.T) {
	e := gb200BridgeEngine(t)
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	serial, ret := e.LookupConfigurableDevice(handle).GetSerial()
	require.Equal(t, nvml.SUCCESS, ret)

	_, ret32 := handleBySerialForTest(serial)
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), ret32)

	_, ret32 = handleBySerialForTest("0000000000000")
	require.Equal(t, uint32(nvml.ERROR_NOT_FOUND), ret32)
}

func TestDeviceGetAddressingModeAndPdi_CheckVersion(t *testing.T) {
	e := gb200BridgeEngine(t)
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	mode, ret32 := addressingModeForTest(handle, nvml.STRUCT_VERSION(nvml.DeviceAddressingMode{}, 1))
	require.Equal(t, uint32(nvml.SUCCESS), ret32)
	require.Equal(t, uint32(nvml.DEVICE_ADDRESSING_MODE_ATS), mode, "a Grace host translates through ATS")
	_, ret32 = addressingModeForTest(handle, 0)
	require.Equal(t, uint32(nvml.ERROR_ARGUMENT_VERSION_MISMATCH), ret32)

	pdi, ret32 := pdiForTest(handle, nvml.STRUCT_VERSION(nvml.Pdi{}, 1))
	require.Equal(t, uint32(nvml.SUCCESS), ret32)
	require.NotZero(t, pdi)
	_, ret32 = pdiForTest(handle, nvml.STRUCT_VERSION(nvml.Pdi{}, 2))
	require.Equal(t, uint32(nvml.ERROR_ARGUMENT_VERSION_MISMATCH), ret32)
}

func TestDeviceGetClkMonStatus_ClearsCallerBuffer(t *testing.T) {
	e := gb200BridgeEngine(t)
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	listSize, dirty, ret32 := clkMonStatusForTest(handle)
	require.Equal(t, uint32(nvml.SUCCESS), ret32)
	require.Zero(t, listSize)
	require.False(t, dirty, "caller garbage survived in the fault list")
}
