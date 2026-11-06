// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// Exercise the file loader and the defaults/override merge, not just Go struct
// literals: omission must survive both stages before a getter sees it.
func presenceEngine(t *testing.T, defaults, override string) *Engine {
	t.Helper()
	indent := func(s string, spaces int) string {
		return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n"+strings.Repeat(" ", spaces))
	}
	doc := "version: '1.0'\nsystem:\n  driver_version: '580.65.06'\n  num_devices: 2\ndevice_defaults:\n  architecture: blackwell\n"
	if defaults != "" {
		doc += "  " + indent(defaults, 2) + "\n"
	}
	doc += "devices:\n  - index: 0\n  - index: 1\n"
	if override != "" {
		doc += "    " + indent(override, 4) + "\n"
	}
	path := filepath.Join(t.TempDir(), "profile.yaml")
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	profile, err := LoadYAMLConfig(path)
	require.NoError(t, err)
	// Node-side profile staging serializes the typed config again. In
	// particular, an explicit zero must not turn into omission on that path.
	raw, err := yaml.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	profile, err = LoadYAMLConfig(path)
	require.NoError(t, err)
	e := NewEngine(&Config{NumDevices: 2, DriverVersion: profile.System.DriverVersion, YAMLConfig: profile})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	return e
}

func presenceDevice(t *testing.T, e *Engine, index int) *ConfigurableDevice {
	t.Helper()
	handle, ret := e.DeviceGetHandleByIndex(index)
	require.Equal(t, nvml.SUCCESS, ret)
	d := e.LookupConfigurableDevice(handle)
	require.NotNil(t, d)
	return d
}

func TestProfileFeaturePresence(t *testing.T) {
	t.Parallel()
	features := []struct {
		name, configured, parentOnly string
		read                         func(*ConfigurableDevice) nvml.Return
	}{
		{"gom", "gpu_operation_mode: {current: all_on}", "", func(d *ConfigurableDevice) nvml.Return {
			_, _, ret := d.GetGpuOperationMode()
			return ret
		}},
		{"retired_v1", "retired_pages: {pending_blacklist: false}", "", func(d *ConfigurableDevice) nvml.Return {
			_, ret := d.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS)
			return ret
		}},
		{"retired_v2", "retired_pages: {}", "", func(d *ConfigurableDevice) nvml.Return {
			_, _, ret := d.GetRetiredPages_v2(nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR)
			return ret
		}},
		{"retired_pending", "retired_pages: {pending_retirement: false}", "", func(d *ConfigurableDevice) nvml.Return {
			_, ret := d.GetRetiredPagesPendingStatus()
			return ret
		}},
		{"dram", "ecc: {dram_encryption: {mode_current: disabled, mode_pending: disabled}}", "ecc: {}", func(d *ConfigurableDevice) nvml.Return {
			_, _, ret := d.GetDramEncryptionMode()
			return ret
		}},
		{"target", "thermal: {target_temperature_c: 0}", "thermal: {max_operating_c: 85}", func(d *ConfigurableDevice) nvml.Return {
			_, ret := d.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR)
			return ret
		}},
		{"sparse", "sparse_operation_mode: false", "", func(d *ConfigurableDevice) nvml.Return {
			_, ret := d.GetSparseOperationMode()
			return ret
		}},
	}
	for _, feature := range features {
		t.Run(feature.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name, defaults, override string
				want                     [2]nvml.Return
			}{
				{"omitted", "", "", [2]nvml.Return{nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_NOT_SUPPORTED}},
				{"parent_only", feature.parentOnly, "", [2]nvml.Return{nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_NOT_SUPPORTED}},
				{"inherited", feature.configured, "", [2]nvml.Return{nvml.SUCCESS, nvml.SUCCESS}},
				{"override_only", "", feature.configured, [2]nvml.Return{nvml.ERROR_NOT_SUPPORTED, nvml.SUCCESS}},
				{"lost_omitted", "failure: {mode: lost}", "", [2]nvml.Return{nvml.ERROR_GPU_IS_LOST, nvml.ERROR_GPU_IS_LOST}},
				{"lost_configured", feature.configured + "\nfailure: {mode: lost}", "", [2]nvml.Return{nvml.ERROR_GPU_IS_LOST, nvml.ERROR_GPU_IS_LOST}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					e := presenceEngine(t, tc.defaults, tc.override)
					// Read the override first to catch mutations leaking into defaults.
					for _, index := range []int{1, 0} {
						require.Equal(t, tc.want[index], feature.read(presenceDevice(t, e, index)))
					}
				})
			}
		})
	}
}

func TestProfileFeatureValues(t *testing.T) {
	t.Parallel()
	e := presenceEngine(t, `gpu_operation_mode: {current: all_on}
sparse_operation_mode: true
retired_pages: {pending_blacklist: false}
ecc: {dram_encryption: {mode_current: enabled, mode_pending: disabled}}
thermal: {target_temperature_c: 73, max_operating_c: 85}`,
		`sparse_operation_mode: false
ecc: {dram_encryption: {mode_current: disabled, mode_pending: enabled}}
thermal: {target_temperature_c: 0, max_operating_c: 85}`)
	for _, index := range []int{1, 0} {
		d := presenceDevice(t, e, index)
		target, ret := d.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR)
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, []uint32{73, 0}[index], target)
		current, pending, ret := d.GetDramEncryptionMode()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, []uint32{1, 0}[index], current.EncryptionState)
		require.Equal(t, []uint32{0, 1}[index], pending.EncryptionState)
		gom, pendingGOM, ret := d.GetGpuOperationMode()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, nvml.GOM_ALL_ON, gom)
		require.Equal(t, gom, pendingGOM)
		sparse, ret := d.GetSparseOperationMode()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, []nvml.EnableState{nvml.FEATURE_ENABLED, nvml.FEATURE_DISABLED}[index], sparse)
		for _, id := range []uint32{fiRetiredSbe, fiRetiredDbe, fiRetiredPending, fiRetiredPendingSbe, fiRetiredPendingDbe} {
			_, value, ret := d.GetFieldValue(id, 0)
			require.Equal(t, nvml.SUCCESS, ret)
			require.Zero(t, value)
		}
		_, ret = d.GetTemperatureThreshold(nvml.TemperatureThresholds(999))
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	}
}

func TestProfileFeatureParentReplacement(t *testing.T) {
	t.Parallel()
	e := presenceEngine(t, "ecc: {dram_encryption: {mode_current: enabled}}\nthermal: {target_temperature_c: 73}", "ecc: {}\nthermal: {max_operating_c: 85}")
	d := presenceDevice(t, e, 1)
	_, _, ret := d.GetDramEncryptionMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = d.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestRetiredPageFields_Omitted(t *testing.T) {
	t.Parallel()
	d := presenceDevice(t, presenceEngine(t, "", ""), 0)
	for _, id := range []uint32{fiRetiredSbe, fiRetiredDbe, fiRetiredPending, fiRetiredPendingSbe, fiRetiredPendingDbe} {
		_, _, ret := d.GetFieldValue(id, 0)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	}
}

func TestDeclaredProfileFeaturesRemainSupported(t *testing.T) {
	t.Parallel()
	for _, source := range profileSources() {
		for sku, path := range source.profiles(t) {
			t.Run(source.label+"/"+sku, func(t *testing.T) {
				t.Parallel()
				cfg, err := LoadYAMLConfig(path)
				require.NoError(t, err)
				d := newTestDeviceWithConfig(t, &cfg.DeviceDefaults)
				if cfg.DeviceDefaults.GPUOperationMode != nil {
					_, _, ret := d.GetGpuOperationMode()
					require.Equal(t, nvml.SUCCESS, ret)
				}
				if cfg.DeviceDefaults.RetiredPages != nil {
					pages, ret := d.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Empty(t, pages)
				}
				if thermal := cfg.DeviceDefaults.Thermal; thermal != nil && thermal.TargetTemperature_C != nil {
					target, ret := d.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, uint32(*thermal.TargetTemperature_C), target)
				}
			})
		}
	}
}

func TestTemperatureThresholds_OnlyModeledKinds(t *testing.T) {
	t.Parallel()
	d := presenceDevice(t, presenceEngine(t, "thermal: {shutdown_threshold_c: 95, slowdown_threshold_c: 90, max_operating_c: 85, target_temperature_c: 73}", ""), 0)
	for _, tc := range []struct {
		kind nvml.TemperatureThresholds
		want uint32
		ret  nvml.Return
	}{
		{nvml.TEMPERATURE_THRESHOLD_SHUTDOWN, 95, nvml.SUCCESS},
		{nvml.TEMPERATURE_THRESHOLD_SLOWDOWN, 90, nvml.SUCCESS},
		{nvml.TEMPERATURE_THRESHOLD_GPU_MAX, 85, nvml.SUCCESS},
		{nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR, 73, nvml.SUCCESS},
		{nvml.TEMPERATURE_THRESHOLD_MEM_MAX, 0, nvml.ERROR_NOT_SUPPORTED},
		{nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_MIN, 0, nvml.ERROR_NOT_SUPPORTED},
		{nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_MAX, 0, nvml.ERROR_NOT_SUPPORTED},
		{nvml.TEMPERATURE_THRESHOLD_GPS_CURR, 0, nvml.ERROR_NOT_SUPPORTED},
		{nvml.TemperatureThresholds(999), 0, nvml.ERROR_NOT_SUPPORTED},
	} {
		value, ret := d.GetTemperatureThreshold(tc.kind)
		require.Equal(t, tc.ret, ret)
		require.Equal(t, tc.want, value)
	}
}

func TestProfileSparseModeMatchesHardwareCapture(t *testing.T) {
	t.Parallel()
	for _, source := range profileSources() {
		for sku, path := range source.profiles(t) {
			t.Run(source.label+"/"+sku, func(t *testing.T) {
				t.Parallel()
				data, err := os.ReadFile(filepath.Join(hardwareCaptureDir(), "qx-"+sku+".xml"))
				require.NoError(t, err)
				var capture struct {
					GPUs []struct {
						SparseMode string `xml:"sparse_operation_mode"`
					} `xml:"gpu"`
				}
				require.NoError(t, xml.Unmarshal(data, &capture))
				require.NotEmpty(t, capture.GPUs)
				cfg, err := LoadYAMLConfig(path)
				require.NoError(t, err)
				mode, ret := newTestDeviceWithConfig(t, &cfg.DeviceDefaults).GetSparseOperationMode()
				if capture.GPUs[0].SparseMode == "N/A" {
					require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
					return
				}
				require.Equal(t, "Disabled", capture.GPUs[0].SparseMode)
				require.Equal(t, nvml.SUCCESS, ret)
				require.Equal(t, nvml.FEATURE_DISABLED, mode)
			})
		}
	}
}
