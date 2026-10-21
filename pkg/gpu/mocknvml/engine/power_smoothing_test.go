// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func smoothingDevice(t *testing.T, smoothing *PowerSmoothingConfig) *ConfigurableDevice {
	t.Helper()
	return newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "blackwell",
		Power: &PowerConfig{
			DefaultLimitMW:  1_000_000,
			EnforcedLimitMW: 1_000_000,
			MinLimitMW:      400_000,
			MaxLimitMW:      1_200_000,
			Smoothing:       smoothing,
		},
	})
}

// smoothingField reads one field the way nvidia-smi does and decodes it by the
// C type nvidia-smi reads it as, which is fixed per field rather than taken
// from the reported value type.
func smoothingUint(t *testing.T, dev *ConfigurableDevice, fieldID, scopeID uint32) uint64 {
	t.Helper()
	vt, val, ret := dev.GetFieldValue(fieldID, scopeID)
	require.Equal(t, nvml.SUCCESS, ret, "field %d", fieldID)
	require.Equal(t, FieldValueUint, vt, "field %d is read as an unsigned int", fieldID)
	return val
}

func smoothingDouble(t *testing.T, dev *ConfigurableDevice, fieldID, scopeID uint32) float64 {
	t.Helper()
	vt, val, ret := dev.GetFieldValue(fieldID, scopeID)
	require.Equal(t, nvml.SUCCESS, ret, "field %d", fieldID)
	require.Equal(t, FieldValueDouble, vt, "field %d is read as a double", fieldID)
	return math.Float64frombits(val)
}

// currentProfileScope is the scope nvidia-smi -q passes for the "Current
// Profile" block: one past the last preset.
const currentProfileScope = 5

// A profile YAML keys presets by their numeric id.
func TestPowerSmoothing_ParsesFromProfileYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`version: "1.0"
system:
  driver_version: "580.65.06"
device_defaults:
  architecture: blackwell
  power:
    power_mizer_mode: prefer_maximum_performance
    smoothing:
      state: enabled
      active_profile: 1
      profiles:
        1:
          percent_tmp_floor: 55.5
          ramp_up_rate_mw_per_s: 30000
          ramp_down_rate_mw_per_s: 15000
          ramp_down_hysteresis_ms: 250
`), 0o600))

	cfg, err := LoadYAMLConfig(path)
	require.NoError(t, err)
	power := cfg.DeviceDefaults.Power
	require.Equal(t, "prefer_maximum_performance", power.PowerMizerMode)
	require.Equal(t, &PowerSmoothingConfig{
		State:         "enabled",
		ActiveProfile: 1,
		Profiles: map[uint32]PowerSmoothingProfileConfig{
			1: {PercentTMPFloor: 55.5, RampUpRateMWPerS: 30000, RampDownRateMWPerS: 15000, RampDownHysteresisMS: 250},
		},
	}, power.Smoothing)
}

// The ids are the simulated 580 driver's, not the pinned CUDA 13.3 header's,
// which moved the whole range down by five. nvidia-smi 580.65.06 asks for
// exactly 256-273.
func TestPowerSmoothing_FieldIDsMatchTheSimulatedDriver(t *testing.T) {
	t.Parallel()
	require.Equal(t, uint32(256), uint32(fiPwrSmoothingEnabled))
	require.Equal(t, uint32(265), uint32(fiPwrSmoothingProfilePercentTMPFloor))
	require.Equal(t, uint32(269), uint32(fiPwrSmoothingActivePresetProfile))
	require.Equal(t, uint32(273), uint32(fiPwrSmoothingAdminOverrideRampDownHyst))
}

// Before Blackwell every field declines, which nvidia-smi renders as
// "Power Smoothing: N/A" — what the A100, H100, L40S and T4 captures report.
func TestPowerSmoothing_DeclinesBeforeBlackwell(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper", Power: &PowerConfig{EnforcedLimitMW: 700_000}})

	for fieldID := uint32(256); fieldID <= 273; fieldID++ {
		_, _, ret := dev.GetFieldValue(fieldID, currentProfileScope)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "field %d", fieldID)
	}
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.PowerSmoothingSetState(&nvml.PowerSmoothingState{State: 1}))
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.PowerSmoothingActivatePresetProfile(&nvml.PowerSmoothingProfile{}))
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.PowerSmoothingUpdatePresetProfileParam(&nvml.PowerSmoothingProfile{}))
}

func TestPowerSmoothing_SupportedOverridesTheArchitecture(t *testing.T) {
	t.Parallel()
	off, on := false, true

	blackwell := smoothingDevice(t, &PowerSmoothingConfig{Supported: &off})
	_, _, ret := blackwell.GetFieldValue(fiPwrSmoothingEnabled, currentProfileScope)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)

	hopper := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Power:        &PowerConfig{Smoothing: &PowerSmoothingConfig{Supported: &on}},
	})
	_, _, ret = hopper.GetFieldValue(fiPwrSmoothingEnabled, currentProfileScope)
	require.Equal(t, nvml.SUCCESS, ret)
}

// A Blackwell board that declares nothing still has the feature, disabled,
// with profile 0 active and no admin overrides.
func TestPowerSmoothing_BlackwellDefaults(t *testing.T) {
	t.Parallel()
	dev := smoothingDevice(t, nil)

	require.Zero(t, smoothingUint(t, dev, fiPwrSmoothingEnabled, currentProfileScope))
	require.Zero(t, smoothingUint(t, dev, fiPwrSmoothingImmRampDownEnabled, currentProfileScope))
	require.Equal(t, uint64(nvml.POWER_SMOOTHING_MAX_NUM_PROFILES),
		smoothingUint(t, dev, fiPwrSmoothingMaxNumPresetProfiles, currentProfileScope))
	require.Zero(t, smoothingUint(t, dev, fiPwrSmoothingActivePresetProfile, currentProfileScope))
	require.InDelta(t, 100.0, smoothingDouble(t, dev, fiPwrSmoothingMaxPercentTMPFloorSetting, currentProfileScope), 0)
	require.Zero(t, smoothingDouble(t, dev, fiPwrSmoothingMinPercentTMPFloorSetting, currentProfileScope))
	require.InDelta(t, 100.0, smoothingDouble(t, dev, fiPwrSmoothingHWCircuitryPercentLifetimeRemaining, currentProfileScope), 0)

	// The privilege level has no documented encoding, so it is left to N/A
	// rather than invented.
	_, _, ret := dev.GetFieldValue(fiPwrSmoothingPrivLvl, currentProfileScope)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// No NVML setter writes an admin override, so all four always carry the
// driver's NOT_SET sentinel, which nvidia-smi renders as "Not Set".
func TestPowerSmoothing_AdminOverridesAreNotSet(t *testing.T) {
	t.Parallel()
	dev := smoothingDevice(t, nil)

	for _, fieldID := range []uint32{
		fiPwrSmoothingAdminOverridePercentTMPFloor,
		fiPwrSmoothingAdminOverrideRampUpRate,
		fiPwrSmoothingAdminOverrideRampDownRate,
	} {
		require.InDelta(t, float64(nvml.POWER_SMOOTHING_ADMIN_OVERRIDE_NOT_SET),
			smoothingDouble(t, dev, fieldID, currentProfileScope), 0, "field %d", fieldID)
	}
	require.Equal(t, uint64(nvml.POWER_SMOOTHING_ADMIN_OVERRIDE_NOT_SET),
		smoothingUint(t, dev, fiPwrSmoothingAdminOverrideRampDownHyst, currentProfileScope))
}

func configuredPresets() *PowerSmoothingConfig {
	return &PowerSmoothingConfig{
		State:         "enabled",
		ActiveProfile: 2,
		Profiles: map[uint32]PowerSmoothingProfileConfig{
			1: {PercentTMPFloor: 40, RampUpRateMWPerS: 20_000, RampDownRateMWPerS: 10_000, RampDownHysteresisMS: 100},
			2: {PercentTMPFloor: 65.5, RampUpRateMWPerS: 30_000, RampDownRateMWPerS: 15_000, RampDownHysteresisMS: 250},
		},
	}
}

// `nvidia-smi power-smoothing -ppd` reads each preset by passing its id as
// the scope.
func TestPowerSmoothing_PresetDefinitionsByScope(t *testing.T) {
	t.Parallel()
	dev := smoothingDevice(t, configuredPresets())

	require.InDelta(t, 40.0, smoothingDouble(t, dev, fiPwrSmoothingProfilePercentTMPFloor, 1), 0)
	require.InDelta(t, 20_000.0, smoothingDouble(t, dev, fiPwrSmoothingProfileRampUpRate, 1), 0)
	require.InDelta(t, 10_000.0, smoothingDouble(t, dev, fiPwrSmoothingProfileRampDownRate, 1), 0)
	require.Equal(t, uint64(100), smoothingUint(t, dev, fiPwrSmoothingProfileRampDownHystVal, 1))

	// An undeclared preset is all zeros rather than a copy of another one.
	require.Zero(t, smoothingDouble(t, dev, fiPwrSmoothingProfilePercentTMPFloor, 4))
	require.Zero(t, smoothingUint(t, dev, fiPwrSmoothingProfileRampDownHystVal, 4))
}

// `nvidia-smi -q` passes one past the last preset and renders the answer as
// the current profile, so that scope resolves to the active preset.
func TestPowerSmoothing_CurrentProfileIsTheActivePreset(t *testing.T) {
	t.Parallel()
	dev := smoothingDevice(t, configuredPresets())

	require.Equal(t, uint64(1), smoothingUint(t, dev, fiPwrSmoothingEnabled, currentProfileScope))
	require.Equal(t, uint64(2), smoothingUint(t, dev, fiPwrSmoothingActivePresetProfile, currentProfileScope))
	require.InDelta(t, 65.5, smoothingDouble(t, dev, fiPwrSmoothingProfilePercentTMPFloor, currentProfileScope), 0)
	require.Equal(t, uint64(250), smoothingUint(t, dev, fiPwrSmoothingProfileRampDownHystVal, currentProfileScope))
}

// The applied ceiling is the enforced power limit, and the floor the active
// preset's percentage of it — but only while smoothing is on.
func TestPowerSmoothing_AppliedTMPFollowsTheLimitAndState(t *testing.T) {
	t.Parallel()

	enabled := smoothingDevice(t, configuredPresets())
	require.InDelta(t, 1000.0, smoothingDouble(t, enabled, fiPwrSmoothingAppliedTMPCeil, currentProfileScope), 0)
	require.InDelta(t, 655.0, smoothingDouble(t, enabled, fiPwrSmoothingAppliedTMPFloor, currentProfileScope), 1e-9)

	disabledCfg := configuredPresets()
	disabledCfg.State = "disabled"
	disabled := smoothingDevice(t, disabledCfg)
	require.InDelta(t, 1000.0, smoothingDouble(t, disabled, fiPwrSmoothingAppliedTMPCeil, currentProfileScope), 0)
	require.Zero(t, smoothingDouble(t, disabled, fiPwrSmoothingAppliedTMPFloor, currentProfileScope))
}

func TestPowerSmoothing_LostGPUReportsLost(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "blackwell",
		Failure:      &FailureInjectionConfig{Mode: FailureModeLost},
	})
	_, _, ret := dev.GetFieldValue(fiPwrSmoothingEnabled, currentProfileScope)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
}

func TestPowerSmoothingSetState_RoundTrips(t *testing.T) {
	persistSetterWrites(t)
	dev := smoothingDevice(t, nil)

	require.Equal(t, nvml.SUCCESS, dev.PowerSmoothingSetState(&nvml.PowerSmoothingState{State: uint32(nvml.FEATURE_ENABLED)}))
	require.Equal(t, uint64(1), smoothingUint(t, dev, fiPwrSmoothingEnabled, currentProfileScope))

	require.Equal(t, nvml.SUCCESS, dev.PowerSmoothingSetState(&nvml.PowerSmoothingState{State: uint32(nvml.FEATURE_DISABLED)}))
	require.Zero(t, smoothingUint(t, dev, fiPwrSmoothingEnabled, currentProfileScope))
}

func TestPowerSmoothingSetState_RejectsInvalidState(t *testing.T) {
	persistSetterWrites(t)
	dev := smoothingDevice(t, nil)

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.PowerSmoothingSetState(nil))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.PowerSmoothingSetState(&nvml.PowerSmoothingState{State: 2}))
	require.Zero(t, smoothingUint(t, dev, fiPwrSmoothingEnabled, currentProfileScope))
}

// The activate call ignores everything but the profile id: nvidia-smi leaves
// paramId and value uninitialised.
func TestPowerSmoothingActivatePresetProfile_RoundTrips(t *testing.T) {
	persistSetterWrites(t)
	dev := smoothingDevice(t, configuredPresets())

	require.Equal(t, nvml.SUCCESS, dev.PowerSmoothingActivatePresetProfile(
		&nvml.PowerSmoothingProfile{ProfileId: 1, ParamId: 0xDEADBEEF, Value: math.NaN()}))

	require.Equal(t, uint64(1), smoothingUint(t, dev, fiPwrSmoothingActivePresetProfile, currentProfileScope))
	require.InDelta(t, 40.0, smoothingDouble(t, dev, fiPwrSmoothingProfilePercentTMPFloor, currentProfileScope), 0,
		"the current profile follows the activated preset")
}

// nvidia-smi does not range-check -spp, so the driver has to.
func TestPowerSmoothingActivatePresetProfile_RejectsUnknownProfile(t *testing.T) {
	persistSetterWrites(t)
	dev := smoothingDevice(t, nil)

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.PowerSmoothingActivatePresetProfile(nil))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
		dev.PowerSmoothingActivatePresetProfile(&nvml.PowerSmoothingProfile{ProfileId: nvml.POWER_SMOOTHING_MAX_NUM_PROFILES}))
	require.Zero(t, smoothingUint(t, dev, fiPwrSmoothingActivePresetProfile, currentProfileScope))
}

// Each parameter lands on its own preset without disturbing the others,
// including the ones the profile YAML declared.
func TestPowerSmoothingUpdatePresetProfileParam_RoundTrips(t *testing.T) {
	persistSetterWrites(t)
	dev := smoothingDevice(t, configuredPresets())

	updates := []nvml.PowerSmoothingProfile{
		{ProfileId: 1, ParamId: nvml.POWER_SMOOTHING_PROFILE_PARAM_PERCENT_TMP_FLOOR, Value: 55.44},
		{ProfileId: 1, ParamId: nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_UP_RATE, Value: 30_000},
		{ProfileId: 3, ParamId: nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_DOWN_RATE, Value: 12_345},
		{ProfileId: 3, ParamId: nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_DOWN_HYSTERESIS, Value: 500},
	}
	for _, u := range updates {
		require.Equal(t, nvml.SUCCESS, dev.PowerSmoothingUpdatePresetProfileParam(&u), "%+v", u)
	}

	require.InDelta(t, 55.44, smoothingDouble(t, dev, fiPwrSmoothingProfilePercentTMPFloor, 1), 0)
	require.InDelta(t, 30_000.0, smoothingDouble(t, dev, fiPwrSmoothingProfileRampUpRate, 1), 0)
	require.InDelta(t, 10_000.0, smoothingDouble(t, dev, fiPwrSmoothingProfileRampDownRate, 1), 0,
		"a parameter nobody wrote keeps its configured value")
	require.Equal(t, uint64(100), smoothingUint(t, dev, fiPwrSmoothingProfileRampDownHystVal, 1))
	require.InDelta(t, 12_345.0, smoothingDouble(t, dev, fiPwrSmoothingProfileRampDownRate, 3), 0)
	require.Equal(t, uint64(500), smoothingUint(t, dev, fiPwrSmoothingProfileRampDownHystVal, 3))
	require.InDelta(t, 65.5, smoothingDouble(t, dev, fiPwrSmoothingProfilePercentTMPFloor, 2), 0,
		"another preset is untouched")
}

func TestPowerSmoothingUpdatePresetProfileParam_RejectsInvalidRequests(t *testing.T) {
	persistSetterWrites(t)
	dev := smoothingDevice(t, nil)

	cases := []struct {
		name string
		p    *nvml.PowerSmoothingProfile
	}{
		{"nil profile", nil},
		{"unknown profile", &nvml.PowerSmoothingProfile{ProfileId: 5, ParamId: 0, Value: 10}},
		// The 580 driver has four parameters; the CUDA 13.3 header's
		// secondary-floor and window parameters do not exist there.
		{"parameter beyond the 580 set", &nvml.PowerSmoothingProfile{ProfileId: 0, ParamId: 4, Value: 10}},
		{"percent above 100", &nvml.PowerSmoothingProfile{ProfileId: 0, ParamId: 0, Value: 100.01}},
		{"negative percent", &nvml.PowerSmoothingProfile{ProfileId: 0, ParamId: 0, Value: -1}},
		{"negative rate", &nvml.PowerSmoothingProfile{ProfileId: 0, ParamId: 1, Value: -1}},
		{"NaN rate", &nvml.PowerSmoothingProfile{ProfileId: 0, ParamId: 2, Value: math.NaN()}},
		{"fractional hysteresis", &nvml.PowerSmoothingProfile{ProfileId: 0, ParamId: 3, Value: 2.5}},
		{"hysteresis beyond uint32", &nvml.PowerSmoothingProfile{ProfileId: 0, ParamId: 3, Value: math.MaxUint32 + 1}},
	}
	for _, tc := range cases {
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.PowerSmoothingUpdatePresetProfileParam(tc.p), tc.name)
	}
	require.Zero(t, smoothingDouble(t, dev, fiPwrSmoothingProfilePercentTMPFloor, 0), "a refused write must not land")
}

func TestPowerSmoothingSetters_DeclineWithoutAWriter(t *testing.T) {
	dev := smoothingDevice(t, nil)

	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.PowerSmoothingSetState(&nvml.PowerSmoothingState{State: 1}))
	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.PowerSmoothingActivatePresetProfile(&nvml.PowerSmoothingProfile{ProfileId: 1}))
	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.PowerSmoothingUpdatePresetProfileParam(
		&nvml.PowerSmoothingProfile{ProfileId: 1, ParamId: 0, Value: 10}))
}
