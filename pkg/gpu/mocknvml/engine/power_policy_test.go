// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Every profile Mokka ships is a datacenter board on mains power.
func TestGetPowerSource_ReportsAC(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"turing", "ampere", "hopper", "blackwell"} {
		dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: arch})
		src, ret := dev.GetPowerSource()
		require.Equal(t, nvml.SUCCESS, ret, arch)
		require.Equal(t, nvml.PowerSource(nvml.POWER_SOURCE_AC), src, arch)
	}
}

func TestGetPowerSource_LostGPUReportsLost(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Failure:      &FailureInjectionConfig{Mode: FailureModeLost},
	})
	_, ret := dev.GetPowerSource()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
}

func allPowerMizerModes() uint32 {
	return 1<<nvml.POWER_MIZER_MODE_ADAPTIVE |
		1<<nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE |
		1<<nvml.POWER_MIZER_MODE_AUTO |
		1<<nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE
}

// A board that declares nothing is driver controlled, which is AUTO.
func TestGetPowerMizerMode_DefaultsToAuto(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})

	modes, ret := dev.GetPowerMizerMode_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(nvml.POWER_MIZER_MODE_AUTO), modes.CurrentMode)
	require.Equal(t, allPowerMizerModes(), modes.SupportedPowerMizerModes)
}

func TestGetPowerMizerMode_ReadsConfiguredMode(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Power:        &PowerConfig{PowerMizerMode: "prefer_maximum_performance"},
	})

	modes, ret := dev.GetPowerMizerMode_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE), modes.CurrentMode)
}

// NVML documents PowerMizer for Maxwell and newer; an unidentified part
// supports nothing.
func TestGetPowerMizerMode_DeclinesBeforeMaxwell(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"kepler", "unknown"} {
		dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: arch})
		_, ret := dev.GetPowerMizerMode_v1()
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, arch)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED,
			dev.SetPowerMizerMode_v1(&nvml.DevicePowerMizerModes_v1{Mode: nvml.POWER_MIZER_MODE_AUTO}), arch)
	}
}

// The mode is driver state, so it has to survive into a getter that reads the
// override document rather than this process's memory.
func TestSetPowerMizerMode_RoundTrips(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})

	require.Equal(t, nvml.SUCCESS, dev.SetPowerMizerMode_v1(
		&nvml.DevicePowerMizerModes_v1{Mode: nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE}))

	modes, ret := dev.GetPowerMizerMode_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE), modes.CurrentMode)
}

func TestSetPowerMizerMode_RejectsInvalidRequests(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetPowerMizerMode_v1(nil))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetPowerMizerMode_v1(&nvml.DevicePowerMizerModes_v1{Mode: 4}))

	modes, ret := dev.GetPowerMizerMode_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(nvml.POWER_MIZER_MODE_AUTO), modes.CurrentMode, "a refused write must not land")
}

func TestPowerMizerSetter_DeclinesWithoutAWriter(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})
	require.Equal(t, nvml.ERROR_NO_PERMISSION,
		dev.SetPowerMizerMode_v1(&nvml.DevicePowerMizerModes_v1{Mode: nvml.POWER_MIZER_MODE_AUTO}))
}
