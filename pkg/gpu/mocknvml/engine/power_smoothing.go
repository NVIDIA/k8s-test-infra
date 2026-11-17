// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Datacenter power smoothing, the Blackwell feature `nvidia-smi
// power-smoothing` drives. NVML has setters for it but no getter: everything
// is read back through nvmlDeviceGetFieldValues, so the field ids below are
// the read side of the three setters in this file.
//
// Only the reported and persisted state is simulated. Enabling smoothing does
// not change the simulated power draw.

import (
	"math"
	"strconv"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// NVML field ids for power smoothing, in the numbering of the simulated 580
// driver. The CUDA 13.3 header go-nvml pins renumbers them five lower, onto
// the ids the 580 driver uses for the clocks-event counters (see
// throttle_counters.go). nvidia-smi 580.65.06 asks for exactly 256-273. Newer
// drivers use the header's numbering; CanonicalFieldID translates it to these.
//
// nvidia-smi reads each field as a fixed C type rather than by the value type
// it is handed, so the uint/double split here is what renders correctly, not
// a choice.
const (
	fiPwrSmoothingEnabled                             = 256 // uint, 0/1
	fiPwrSmoothingPrivLvl                             = 257 // uint
	fiPwrSmoothingImmRampDownEnabled                  = 258 // uint, 0/1
	fiPwrSmoothingAppliedTMPCeil                      = 259 // double, W
	fiPwrSmoothingAppliedTMPFloor                     = 260 // double, W
	fiPwrSmoothingMaxPercentTMPFloorSetting           = 261 // double, %
	fiPwrSmoothingMinPercentTMPFloorSetting           = 262 // double, %
	fiPwrSmoothingHWCircuitryPercentLifetimeRemaining = 263 // double, %
	fiPwrSmoothingMaxNumPresetProfiles                = 264 // uint
	fiPwrSmoothingProfilePercentTMPFloor              = 265 // double, %
	fiPwrSmoothingProfileRampUpRate                   = 266 // double, mW/s
	fiPwrSmoothingProfileRampDownRate                 = 267 // double, mW/s
	fiPwrSmoothingProfileRampDownHystVal              = 268 // uint, ms
	fiPwrSmoothingActivePresetProfile                 = 269 // uint
	fiPwrSmoothingAdminOverridePercentTMPFloor        = 270 // double, %
	fiPwrSmoothingAdminOverrideRampUpRate             = 271 // double, mW/s
	fiPwrSmoothingAdminOverrideRampDownRate           = 272 // double, mW/s
	fiPwrSmoothingAdminOverrideRampDownHyst           = 273 // uint, ms
)

const (
	powerSmoothingMaxProfiles = nvml.POWER_SMOOTHING_MAX_NUM_PROFILES

	// The NVML-documented range for the percent TMP floor parameter.
	powerSmoothingMinPercentTMPFloor = 0.0
	powerSmoothingMaxPercentTMPFloor = 100.0
)

// PowerSmoothingParam is an NVML_POWER_SMOOTHING_PROFILE_PARAM_* value. The 580
// driver defines four; the four the CUDA 13.3 header adds after them do not
// exist there.
type PowerSmoothingParam uint32

// The preset profile parameters the 580 driver defines.
const (
	PowerSmoothingParamPercentTMPFloor    PowerSmoothingParam = nvml.POWER_SMOOTHING_PROFILE_PARAM_PERCENT_TMP_FLOOR
	PowerSmoothingParamRampUpRate         PowerSmoothingParam = nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_UP_RATE
	PowerSmoothingParamRampDownRate       PowerSmoothingParam = nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_DOWN_RATE
	PowerSmoothingParamRampDownHysteresis PowerSmoothingParam = nvml.POWER_SMOOTHING_PROFILE_PARAM_RAMP_DOWN_HYSTERESIS
)

// ConfigKey is the PowerSmoothingProfileConfig field the parameter is recorded
// under.
func (p PowerSmoothingParam) ConfigKey() string {
	switch p {
	case PowerSmoothingParamPercentTMPFloor:
		return "percent_tmp_floor"
	case PowerSmoothingParamRampUpRate:
		return "ramp_up_rate_mw_per_s"
	case PowerSmoothingParamRampDownRate:
		return "ramp_down_rate_mw_per_s"
	case PowerSmoothingParamRampDownHysteresis:
		return "ramp_down_hysteresis_ms"
	default:
		return ""
	}
}

// powerSmoothing returns the device's smoothing config and whether the device
// has the feature at all: Blackwell and newer unless the config says otherwise.
// The config is never nil, so callers read defaults from it directly.
func (d *ConfigurableDevice) powerSmoothing() (*PowerSmoothingConfig, bool) {
	supported := gpuarch.Arch(d.Config.Architecture).AtLeast(gpuarch.Blackwell)
	cfg := &PowerSmoothingConfig{}
	if c := d.cfg(); c.Power != nil && c.Power.Smoothing != nil {
		cfg = c.Power.Smoothing
		if cfg.Supported != nil {
			supported = *cfg.Supported
		}
	}
	return cfg, supported
}

func (c *PowerSmoothingConfig) enabled() bool {
	return c.State == "enabled"
}

func (c *PowerSmoothingConfig) profile(id uint32) PowerSmoothingProfileConfig {
	return c.Profiles[id]
}

func isPowerSmoothingField(fieldID uint32) bool {
	return fieldID >= fiPwrSmoothingEnabled && fieldID <= fiPwrSmoothingAdminOverrideRampDownHyst
}

// powerSmoothingFieldValue resolves one of the ids isPowerSmoothingField
// accepts.
//
// scopeID selects the preset for the per-profile fields. `nvidia-smi
// power-smoothing -ppd` passes the preset id; `nvidia-smi -q` passes one past
// the last preset and renders the answer as the current profile, so any scope
// outside the presets resolves to the active one.
//
//nolint:cyclop // one case per field id; splitting it would only scatter the table
func (d *ConfigurableDevice) powerSmoothingFieldValue(fieldID, scopeID uint32) (FieldValueType, uint64, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return FieldValueUnsupported, 0, ret
	}
	cfg, supported := d.powerSmoothing()
	if !supported {
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
	}

	profileID := scopeID
	if profileID >= powerSmoothingMaxProfiles {
		profileID = cfg.ActiveProfile
	}
	preset := cfg.profile(profileID)

	switch fieldID {
	case fiPwrSmoothingEnabled:
		return FieldValueUint, boolField(cfg.enabled()), nvml.SUCCESS
	case fiPwrSmoothingPrivLvl:
		// NVML documents no encoding for the privilege level, so it stays
		// N/A rather than being invented.
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED
	case fiPwrSmoothingImmRampDownEnabled:
		return FieldValueUint, 0, nvml.SUCCESS
	case fiPwrSmoothingAppliedTMPCeil, fiPwrSmoothingAppliedTMPFloor:
		ceilMW, ret := d.GetEnforcedPowerLimit()
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret
		}
		watts := float64(ceilMW) / 1000
		if fieldID == fiPwrSmoothingAppliedTMPFloor {
			watts = 0
			if cfg.enabled() {
				watts = float64(ceilMW) / 1000 * cfg.profile(cfg.ActiveProfile).PercentTMPFloor / 100
			}
		}
		return doubleField(watts)
	case fiPwrSmoothingMaxPercentTMPFloorSetting:
		return doubleField(powerSmoothingMaxPercentTMPFloor)
	case fiPwrSmoothingMinPercentTMPFloorSetting:
		return doubleField(powerSmoothingMinPercentTMPFloor)
	case fiPwrSmoothingHWCircuitryPercentLifetimeRemaining:
		return doubleField(100)
	case fiPwrSmoothingMaxNumPresetProfiles:
		return FieldValueUint, powerSmoothingMaxProfiles, nvml.SUCCESS
	case fiPwrSmoothingProfilePercentTMPFloor:
		return doubleField(preset.PercentTMPFloor)
	case fiPwrSmoothingProfileRampUpRate:
		return doubleField(preset.RampUpRateMWPerS)
	case fiPwrSmoothingProfileRampDownRate:
		return doubleField(preset.RampDownRateMWPerS)
	case fiPwrSmoothingProfileRampDownHystVal:
		return FieldValueUint, uint64(preset.RampDownHysteresisMS), nvml.SUCCESS
	case fiPwrSmoothingActivePresetProfile:
		return FieldValueUint, uint64(cfg.ActiveProfile), nvml.SUCCESS
	case fiPwrSmoothingAdminOverrideRampDownHyst:
		// No NVML setter writes an admin override, so all four always carry
		// the driver's sentinel, which nvidia-smi renders as "Not Set".
		return FieldValueUint, nvml.POWER_SMOOTHING_ADMIN_OVERRIDE_NOT_SET, nvml.SUCCESS
	default: // the three double admin overrides
		return doubleField(nvml.POWER_SMOOTHING_ADMIN_OVERRIDE_NOT_SET)
	}
}

func doubleField(v float64) (FieldValueType, uint64, nvml.Return) {
	return FieldValueDouble, math.Float64bits(v), nvml.SUCCESS
}

// PowerSmoothingSetState enables or disables power smoothing. Like every
// setter in the power family the state is recorded in the override document,
// so `nvidia-smi power-smoothing -s 1` followed by a separate `nvidia-smi -q`
// reports it.
func (d *ConfigurableDevice) PowerSmoothingSetState(state *nvml.PowerSmoothingState) nvml.Return {
	const fn = "nvmlDevicePowerSmoothingSetState"
	if state == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if ret := d.powerSmoothingSetterGuard(fn); ret != nvml.SUCCESS {
		return ret
	}
	if state.State != uint32(nvml.FEATURE_DISABLED) && state.State != uint32(nvml.FEATURE_ENABLED) {
		debugLog("[NVML] %s(%d) -> INVALID_ARGUMENT\n", fn, state.State)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	enabled := state.State == uint32(nvml.FEATURE_ENABLED)
	return d.persistPowerSmoothing(fn, func(w OverrideWriter) error {
		return w.SetPowerSmoothingState(d.PhysicalIndex(), enabled)
	})
}

// PowerSmoothingActivatePresetProfile makes profile.ProfileId the active
// preset. NVML reads nothing else from the struct, and nvidia-smi leaves the
// rest uninitialised.
func (d *ConfigurableDevice) PowerSmoothingActivatePresetProfile(profile *nvml.PowerSmoothingProfile) nvml.Return {
	const fn = "nvmlDevicePowerSmoothingActivatePresetProfile"
	if profile == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if ret := d.powerSmoothingSetterGuard(fn); ret != nvml.SUCCESS {
		return ret
	}
	// nvidia-smi does not range-check -spp, so this is the only check.
	if profile.ProfileId >= powerSmoothingMaxProfiles {
		debugLog("[NVML] %s(profile=%d) -> INVALID_ARGUMENT\n", fn, profile.ProfileId)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return d.persistPowerSmoothing(fn, func(w OverrideWriter) error {
		return w.SetPowerSmoothingActiveProfile(d.PhysicalIndex(), profile.ProfileId)
	})
}

// PowerSmoothingUpdatePresetProfileParam sets one parameter of one preset.
// Ramp rates are taken in mW/s, the unit nvidia-smi passes through from -rur
// and -rdr and the one the profile fields report.
func (d *ConfigurableDevice) PowerSmoothingUpdatePresetProfileParam(profile *nvml.PowerSmoothingProfile) nvml.Return {
	const fn = "nvmlDevicePowerSmoothingUpdatePresetProfileParam"
	if profile == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if ret := d.powerSmoothingSetterGuard(fn); ret != nvml.SUCCESS {
		return ret
	}
	param := PowerSmoothingParam(profile.ParamId)
	if profile.ProfileId >= powerSmoothingMaxProfiles || !validPowerSmoothingParam(param, profile.Value) {
		debugLog("[NVML] %s(profile=%d, param=%d, value=%v) -> INVALID_ARGUMENT\n",
			fn, profile.ProfileId, profile.ParamId, profile.Value)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return d.persistPowerSmoothing(fn, func(w OverrideWriter) error {
		return w.SetPowerSmoothingProfileParam(d.PhysicalIndex(), profile.ProfileId, param, profile.Value)
	})
}

// validPowerSmoothingParam reports whether value is something param can hold.
// The hysteresis is whole milliseconds, so a fraction is refused rather than
// silently truncated.
func validPowerSmoothingParam(param PowerSmoothingParam, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	switch param {
	case PowerSmoothingParamPercentTMPFloor:
		return value >= powerSmoothingMinPercentTMPFloor && value <= powerSmoothingMaxPercentTMPFloor
	case PowerSmoothingParamRampUpRate, PowerSmoothingParamRampDownRate:
		return value >= 0
	case PowerSmoothingParamRampDownHysteresis:
		return value >= 0 && value <= math.MaxUint32 && value == math.Trunc(value)
	default:
		return false
	}
}

// powerSmoothingSetterGuard runs the checks every smoothing setter shares
// before it looks at its arguments.
func (d *ConfigurableDevice) powerSmoothingSetterGuard(fn string) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		debugLog("[NVML] %s -> %d (injected failure)\n", fn, ret)
		return ret
	}
	if _, supported := d.powerSmoothing(); !supported {
		debugLog("[NVML] %s -> NOT_SUPPORTED\n", fn)
		return nvml.ERROR_NOT_SUPPORTED
	}
	return nvml.SUCCESS
}

func (d *ConfigurableDevice) persistPowerSmoothing(fn string, write func(OverrideWriter) error) nvml.Return {
	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] %s -> NO_PERMISSION (no override writer)\n", fn)
		return nvml.ERROR_NO_PERMISSION
	}
	if err := write(w); err != nil {
		warnLog("[NVML] %s -> NO_PERMISSION: %v\n", fn, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] %s -> written\n", fn)
	return nvml.SUCCESS
}

// PowerSmoothingProfilePatch builds the override patch recording one preset
// parameter. It lives here, beside ConfigKey, so the writer and the config
// type cannot disagree on the shape.
func PowerSmoothingProfilePatch(profileID uint32, param PowerSmoothingParam, value float64) map[string]any {
	var v any = value
	if param == PowerSmoothingParamRampDownHysteresis {
		v = uint32(value)
	}
	return map[string]any{"power": map[string]any{"smoothing": map[string]any{
		"profiles": map[string]any{strconv.FormatUint(uint64(profileID), 10): map[string]any{param.ConfigKey(): v}},
	}}}
}
