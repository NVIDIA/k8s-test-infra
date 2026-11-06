// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
	"github.com/stretchr/testify/require"
)

// shippedProfileDevice starts an engine on a shipped profile, with the chart's
// driverVersion set to driver when it is not empty, and returns GPU 0. edit, if
// not nil, changes the loaded profile first.
func shippedProfileDevice(t *testing.T, profile, driver string, edit ...func(*YAMLConfig)) *ConfigurableDevice {
	t.Helper()
	yc, err := LoadYAMLConfig(filepath.Join("..", "configs", "mock-nvml-config-"+profile+".yaml"))
	require.NoError(t, err)
	for _, f := range edit {
		f(yc)
	}
	e := NewEngine(&Config{NumDevices: len(yc.Devices), DriverVersion: driver, YAMLConfig: yc})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	return e.LookupConfigurableDevice(handle)
}

func absentFeatures(value string) func(*YAMLConfig) {
	return func(yc *YAMLConfig) { yc.System.AbsentFeatures = value }
}

// absentFeatureReturns is what each call gated on a profile key answers.
func absentFeatureReturns(d *ConfigurableDevice) map[string]nvml.Return {
	_, _, gom := d.GetGpuOperationMode()
	_, retired := d.GetRetiredPages(nvml.DOUBLE_BIT_ECC)
	_, _, retiredV2 := d.GetRetiredPages_v2(nvml.DOUBLE_BIT_ECC)
	_, pending := d.GetRetiredPagesPendingStatus()
	_, _, dram := d.GetDramEncryptionMode()
	_, memMax := d.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_MEM_MAX)
	_, acoustic := d.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_MAX)
	_, gps := d.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_GPS_CURR)
	return map[string]nvml.Return{
		"GetGpuOperationMode":               gom,
		"GetRetiredPages":                   retired,
		"GetRetiredPages_v2":                retiredV2,
		"GetRetiredPagesPendingStatus":      pending,
		"GetDramEncryptionMode":             dram,
		"GetTemperatureThreshold(MEM_MAX)":  memMax,
		"GetTemperatureThreshold(ACOUSTIC)": acoustic,
		"GetTemperatureThreshold(GPS)":      gps,
	}
}

// The real Vera Rubin tray answers NOT_SUPPORTED for every feature vrnvl72
// leaves out, while the thresholds it has still answer. The profile sets
// absent_features: not_supported, so the driver it runs on does not matter.
func TestAbsentFeatures_NotSupportedProfile(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"", "580.65.06"} {
		d := shippedProfileDevice(t, "vrnvl72", driver)
		for call, ret := range absentFeatureReturns(d) {
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "driver %q: %s", driver, call)
		}
		for _, th := range []nvml.TemperatureThresholds{
			nvml.TEMPERATURE_THRESHOLD_SHUTDOWN, nvml.TEMPERATURE_THRESHOLD_SLOWDOWN, nvml.TEMPERATURE_THRESHOLD_GPU_MAX,
		} {
			_, ret := d.GetTemperatureThreshold(th)
			require.Equal(t, nvml.SUCCESS, ret, "driver %q: threshold %d", driver, th)
		}
	}
	caps, ret := shippedProfileDevice(t, "vrnvl72", "").GetCapabilities()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(nvml.DEV_CAP_EGM), caps.CapMask, "vrnvl72 declares egm")
}

// The other profiles keep the legacy defaults on any driver, 615 included.
func TestAbsentFeatures_LegacyProfilesKeepTheirAnswers(t *testing.T) {
	t.Parallel()
	for _, profile := range []string{"a100", "h100", "b200", "gb200", "gb300", "l40s", "t4"} {
		for _, driver := range []string{"", "615.71.09"} {
			t.Run(profile+"/"+driver, func(t *testing.T) {
				t.Parallel()
				d := shippedProfileDevice(t, profile, driver)
				for call, ret := range absentFeatureReturns(d) {
					if call == "GetDramEncryptionMode" && !d.archAtLeast(gpuarch.Blackwell) {
						require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "%s: before Blackwell", call)
						continue
					}
					require.Equal(t, nvml.SUCCESS, ret, call)
				}
				caps, ret := d.GetCapabilities()
				require.Equal(t, nvml.SUCCESS, ret)
				require.Zero(t, caps.CapMask, "no egm")
			})
		}
	}
}

// Under not_supported a profile keeps what it declares and loses only what it
// leaves out.
func TestAbsentFeatures_DeclaredFeaturesStillAnswer(t *testing.T) {
	t.Parallel()
	d := shippedProfileDevice(t, "gb300", "", absentFeatures(AbsentFeaturesNotSupported))
	got := absentFeatureReturns(d)
	require.Equal(t, nvml.SUCCESS, got["GetGpuOperationMode"], "gb300 declares gpu_operation_mode")
	require.Equal(t, nvml.SUCCESS, got["GetRetiredPages"], "gb300 declares retired_pages")
	require.Equal(t, nvml.SUCCESS, got["GetTemperatureThreshold(MEM_MAX)"], "gb300 declares temperature_memory_c")
	require.Equal(t, nvml.SUCCESS, got["GetTemperatureThreshold(ACOUSTIC)"], "gb300 declares target_temperature_c")
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, got["GetTemperatureThreshold(GPS)"], "no profile key declares the GPS threshold")
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, got["GetDramEncryptionMode"], "gb300 has no ecc.dram_encryption block")
}

// An unknown value keeps the legacy defaults instead of failing the engine.
func TestAbsentFeatures_UnknownValueKeepsLegacyDefaults(t *testing.T) {
	t.Parallel()
	d := shippedProfileDevice(t, "gb300", "", absentFeatures("strict"))
	require.Equal(t, nvml.SUCCESS, absentFeatureReturns(d)["GetDramEncryptionMode"])
}

// With accounting disabled, a not_supported profile lists no processes and
// finds no record, where legacy profiles keep the mock's NOT_SUPPORTED.
func TestAbsentFeatures_DisabledAccounting(t *testing.T) {
	t.Parallel()
	for profile, want := range map[string][2]nvml.Return{
		"vrnvl72": {nvml.SUCCESS, nvml.ERROR_NOT_FOUND},
		"gb300":   {nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_NOT_SUPPORTED},
	} {
		d := shippedProfileDevice(t, profile, "615.71.09")
		pids, ret := d.GetAccountingPids()
		require.Equal(t, want[0], ret, "%s: GetAccountingPids", profile)
		require.Empty(t, pids, profile)
		_, ret = d.GetAccountingStats(1)
		require.Equal(t, want[1], ret, "%s: GetAccountingStats", profile)
	}
}

// A devices[] entry can turn EGM on for one GPU.
func TestGetCapabilities_PerDeviceEGM(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{
		NumDevices:    2,
		DriverVersion: "580.65.06",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System:  SystemConfig{DriverVersion: "580.65.06", NumDevices: 2},
			Devices: []DeviceOverride{{Index: 1, DeviceConfig: DeviceConfig{EGM: true}}},
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	for i, want := range []uint32{0, nvml.DEV_CAP_EGM} {
		handle, ret := e.DeviceGetHandleByIndex(i)
		require.Equal(t, nvml.SUCCESS, ret)
		caps, ret := e.LookupConfigurableDevice(handle).GetCapabilities()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, want, caps.CapMask, "GPU %d", i)
	}
}
