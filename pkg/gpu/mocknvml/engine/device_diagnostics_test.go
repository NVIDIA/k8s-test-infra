// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Clock monitors arrived with Ampere; a healthy one reports no faulted domain.
func TestConfigurableDevice_GetClkMonStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		arch string
		ret  nvml.Return
	}{
		{arch: "turing", ret: nvml.ERROR_NOT_SUPPORTED},
		{arch: "ampere", ret: nvml.SUCCESS},
		{arch: "blackwell", ret: nvml.SUCCESS},
	} {
		t.Run(tc.arch, func(t *testing.T) {
			t.Parallel()
			e := pcieIdentityEngine(t, DeviceConfig{Architecture: tc.arch}, DeviceOverride{Index: 0})

			status, ret := pcieIdentityDevice(t, e, 0).GetClkMonStatus()
			require.Equal(t, tc.ret, ret)
			require.Zero(t, status.BGlobalStatus, "no fault")
			require.Zero(t, status.ClkMonListSize, "no faulted domains")
		})
	}
}

// timeRun is the time the driver has been loaded, so it is read off the same
// shared epoch that NVLink counters accrue from: every process agrees on it
// and it grows between invocations.
func TestConfigurableDevice_GetBBXTimeData(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})
	dev := pcieIdentityDevice(t, e, 0)
	loaded := time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC)
	dev.fabric.epoch = loaded
	dev.fabric.now = func() time.Time { return loaded.Add(90*time.Minute + 500*time.Millisecond) }

	data, ret := dev.GetBBXTimeData_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(90*60), data.TimeRun)
}

// The black box flushes on a schedule the driver keeps; a freshly loaded
// driver has not flushed yet, which is what the A100, B200, L40S and T4
// captures report.
func TestConfigurableDevice_GetLastBBXFlushTime(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})

	_, _, ret := pcieIdentityDevice(t, e, 0).GetLastBBXFlushTime()
	require.Equal(t, nvml.ERROR_NOT_READY, ret)
}

// The mock has no model of the NVLink physical layer PRM registers expose, so
// both PRM entry points refuse — even on Blackwell, where real hardware has them.
func TestConfigurableDevice_PRMAccessDeclines(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{Architecture: "blackwell"}, DeviceOverride{Index: 0})
	dev := pcieIdentityDevice(t, e, 0)

	_, ret := dev.ReadPRMCounters_v1([]nvml.PRMCounterId{1}, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "ReadPRMCounters_v1")

	ret = dev.ReadWritePRM_v1(&nvml.PRMTLV_v1{})
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "ReadWritePRM_v1")
}

// Nothing the mock reports accumulates in a way a caller can reset, so each
// field declines individually while the call itself succeeds — the per-value
// contract nvml.h documents.
func TestConfigurableDevice_ClearFieldValues(t *testing.T) {
	t.Parallel()
	e := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0})
	values := []nvml.FieldValue{
		{FieldId: nvml.FI_DEV_NVLINK_CRC_FLIT_ERROR_COUNT_TOTAL},
		{FieldId: nvml.FI_DEV_ECC_SBE_VOL_TOTAL},
	}

	ret := pcieIdentityDevice(t, e, 0).ClearFieldValues(values)
	require.Equal(t, nvml.SUCCESS, ret)
	for _, v := range values {
		require.Equal(t, uint32(nvml.ERROR_NOT_SUPPORTED), v.NvmlReturn, "field %d", v.FieldId)
		require.NotZero(t, v.Timestamp, "field %d", v.FieldId)
	}
}
