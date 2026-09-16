// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// fanlessConfig is the shape every profile Mokka ships declares, and what all
// seven hardware captures report: a board with no dedicated fans.
func fanlessConfig() *DeviceConfig {
	return &DeviceConfig{Fan: &FanConfig{
		Count:              0,
		SpeedPercent:       "N/A",
		TargetSpeedPercent: "N/A",
	}}
}

// fannedConfig models a discrete board with two measured fans, which is the
// only shape under which any of this family answers. The numbers are distinct
// so a getter reading a neighbouring field fails rather than coincidentally
// agreeing: the duty cycle, the target, the floor and the ceiling are four
// different values.
func fannedConfig() *DeviceConfig {
	return &DeviceConfig{Fan: &FanConfig{
		Count:              2,
		SpeedPercent:       "41",
		TargetSpeedPercent: "55",
		MaxSpeedRPM:        4000,
		MinSpeedPercent:    23,
		MaxSpeedPercent:    97,
	}}
}

// TestFanFamily_RefusedOnAFanlessBoard pins the answer every shipped profile
// must give. It matters that this is a refusal derived from the profile rather
// than from the export being unwritten: NOT_SUPPORTED is what a real SXM or
// passively-cooled PCIe board returns, and it is what a consumer renders as
// N/A instead of inventing a reading.
func TestFanFamily_RefusedOnAFanlessBoard(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fanlessConfig())

	_, ret := dev.FanSpeedRPM(0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "FanSpeedRPM")
	_, ret = dev.GetTargetFanSpeed(0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetTargetFanSpeed")
	_, _, ret = dev.GetMinMaxFanSpeed()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetMinMaxFanSpeed")
	_, ret = dev.CoolerInfo(0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "CoolerInfo")
	_, ret = dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetFanControlPolicy_v2")

	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetFanSpeed_v2(0, 50), "SetFanSpeed_v2")
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetDefaultFanSpeed_v2(0), "SetDefaultFanSpeed_v2")
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED,
		dev.SetFanControlPolicy(0, nvml.FAN_POLICY_MANUAL), "SetFanControlPolicy")
}

// TestFanFamily_NoFanConfigIsAlsoFanless covers a profile that omits the fan
// block entirely rather than declaring count 0. Both mean the same board.
func TestFanFamily_NoFanConfigIsAlsoFanless(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	_, ret := dev.FanSpeedRPM(0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "FanSpeedRPM")
	_, _, ret = dev.GetMinMaxFanSpeed()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "GetMinMaxFanSpeed")
}

func TestGetFanSpeedRPM_ScalesTheDutyCycleByTheTachometerCeiling(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fannedConfig())

	rpm, ret := dev.FanSpeedRPM(1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1640), rpm, "41 %% of a 4000 RPM ceiling")
}

// TestGetFanSpeedRPM_RefusedWithoutATachometer separates the two readings a
// real board reports independently: it can drive a fan without measuring it,
// in which case the percentage answers and the RPM does not.
func TestGetFanSpeedRPM_RefusedWithoutATachometer(t *testing.T) {
	t.Parallel()
	cfg := fannedConfig()
	cfg.Fan.MaxSpeedRPM = 0
	dev := newTestDeviceWithConfig(t, cfg)

	_, ret := dev.FanSpeedRPM(0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret,
		"a board with no tachometer must refuse the RPM reading")

	speed, ret := dev.GetFanSpeed_v2(0)
	require.Equal(t, nvml.SUCCESS, ret, "the percentage is a separate reading and still answers")
	require.Equal(t, uint32(41), speed)
}

// TestGetTargetFanSpeed_ReadsTheConfiguredTarget covers the field this family's
// only pre-existing config key names. target_speed_percent is declared by every
// profile in the repo and was read by nothing before this getter existed.
func TestGetTargetFanSpeed_ReadsTheConfiguredTarget(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fannedConfig())

	target, ret := dev.GetTargetFanSpeed(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 55, target, "the target is its own reading, not the current duty cycle")
}

func TestGetTargetFanSpeed_RefusedWhenTheProfileDeclaresNone(t *testing.T) {
	t.Parallel()
	cfg := fannedConfig()
	cfg.Fan.TargetSpeedPercent = "N/A"
	dev := newTestDeviceWithConfig(t, cfg)

	_, ret := dev.GetTargetFanSpeed(0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestGetMinMaxFanSpeed_ReportsTheConfiguredEnvelope(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fannedConfig())

	minSpeed, maxSpeed, ret := dev.GetMinMaxFanSpeed()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 23, minSpeed)
	require.Equal(t, 97, maxSpeed)
}

// TestGetMinMaxFanSpeed_DefaultsTheCeilingTo100 keeps a fanned profile that
// declares no envelope reporting the 0-100 window a discrete board reports,
// rather than a zero-width one that would refuse every manual write.
func TestGetMinMaxFanSpeed_DefaultsTheCeilingTo100(t *testing.T) {
	t.Parallel()
	cfg := fannedConfig()
	cfg.Fan.MinSpeedPercent, cfg.Fan.MaxSpeedPercent = 0, 0
	dev := newTestDeviceWithConfig(t, cfg)

	minSpeed, maxSpeed, ret := dev.GetMinMaxFanSpeed()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 0, minSpeed)
	require.Equal(t, 100, maxSpeed)
}

func TestGetCoolerInfo_DescribesAVariableGpuCooler(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fannedConfig())

	info, ret := dev.CoolerInfo(1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), info.Index, "the caller's index is echoed back")
	require.Equal(t, uint32(nvml.THERMAL_COOLER_SIGNAL_VARIABLE), info.SignalType,
		"a fan driven to a duty cycle is a variable cooler, not a toggle")
	require.Equal(t, uint32(nvml.THERMAL_COOLER_TARGET_GPU), info.Target)
}

// TestFanFamily_RejectsAFanTheBoardDoesNotHave covers the index bound on every
// per-fan entry point. Answering for fan 5 on a two-fan board is how a mock
// invents hardware.
func TestFanFamily_RejectsAFanTheBoardDoesNotHave(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fannedConfig())

	const absent = 2 // the board declares fans 0 and 1

	_, ret := dev.FanSpeedRPM(absent)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "FanSpeedRPM")
	_, ret = dev.GetTargetFanSpeed(absent)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "GetTargetFanSpeed")
	_, ret = dev.CoolerInfo(absent)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "CoolerInfo")
	_, ret = dev.GetFanControlPolicy_v2(absent)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "GetFanControlPolicy_v2")
	_, ret = dev.GetFanSpeed_v2(absent)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "GetFanSpeed_v2")

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetFanSpeed_v2(absent, 50), "SetFanSpeed_v2")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetDefaultFanSpeed_v2(absent), "SetDefaultFanSpeed_v2")

	_, ret = dev.FanSpeedRPM(-1)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "a negative fan index names no fan either")
}

// TestGetFanControlPolicy_DefaultsToTheDriverCurve pins the policy a board
// reports before anything writes one: the driver's own temperature curve, not
// manual.
func TestGetFanControlPolicy_DefaultsToTheDriverCurve(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fannedConfig())

	policy, ret := dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW), policy)
}

// TestGetFanControlPolicy_ReadsAProfileDeclaredControl covers a profile
// modelling a board that ships under manual control, which is also the shape
// the setters persist.
func TestGetFanControlPolicy_ReadsAProfileDeclaredControl(t *testing.T) {
	t.Parallel()
	seventy := 70
	cfg := fannedConfig()
	cfg.Fan.Controls = []FanControlConfig{{Fan: 1, Policy: "manual", SpeedPercent: &seventy}}
	dev := newTestDeviceWithConfig(t, cfg)

	policy, ret := dev.GetFanControlPolicy_v2(1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_MANUAL), policy)

	speed, ret := dev.GetFanSpeed_v2(1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(70), speed, "under manual control the requested duty cycle is what the fan runs at")

	// The entry names fan 1 only, so fan 0 must still report the board's own
	// reading rather than inheriting its neighbour's.
	policy, ret = dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW), policy)
	speed, ret = dev.GetFanSpeed_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(41), speed)
}

// TestSetFanSpeed_RoundTripsThroughTheOverrideDocument is the assertion that
// separates a persisted write from one kept in the writing process's memory:
// the value is read back through the config the engine re-reads, which is the
// state every other process on the node sees too.
func TestSetFanSpeed_RoundTripsThroughTheOverrideDocument(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, fannedConfig())

	require.Equal(t, nvml.SUCCESS, dev.SetFanSpeed_v2(0, 88))

	policy, ret := dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_MANUAL), policy,
		"asking for a duty cycle puts the fan under manual control")

	target, ret := dev.GetTargetFanSpeed(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 88, target, "the write is what the fan is now aiming for")

	speed, ret := dev.GetFanSpeed_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(88), speed)

	rpm, ret := dev.FanSpeedRPM(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(3520), rpm, "88 %% of a 4000 RPM ceiling")
}

// TestSetFanSpeed_LeavesTheOtherFanAlone covers the per-fan scope of the write.
// A setter that recorded one duty cycle for the whole board would pass every
// assertion above and fail this one.
func TestSetFanSpeed_LeavesTheOtherFanAlone(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, fannedConfig())

	require.Equal(t, nvml.SUCCESS, dev.SetFanSpeed_v2(1, 90))

	speed, ret := dev.GetFanSpeed_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(41), speed, "fan 0 was not written and keeps the board's reading")
	policy, ret := dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW), policy)

	speed, ret = dev.GetFanSpeed_v2(1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(90), speed)
}

// TestSetFanSpeed_RefusesOutsideTheReportedEnvelope keeps the write honest
// against the window GetMinMaxFanSpeed advertises: a board that accepts a duty
// cycle it just said it could not reach is lying in one of the two places.
func TestSetFanSpeed_RefusesOutsideTheReportedEnvelope(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, fannedConfig())

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetFanSpeed_v2(0, 22),
		"22 %% is below the advertised floor of 23 %%")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetFanSpeed_v2(0, 98),
		"98 %% is above the advertised ceiling of 97 %%")

	policy, ret := dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW), policy,
		"a refused write must leave the fan under its previous policy")
}

// TestSetDefaultFanSpeed_ReturnsTheFanToTheDriverCurve covers the undo half of
// the pair. Restoring the policy without dropping the recorded duty cycle would
// leave the fan reporting a manual speed it is no longer being held at.
func TestSetDefaultFanSpeed_ReturnsTheFanToTheDriverCurve(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, fannedConfig())

	require.Equal(t, nvml.SUCCESS, dev.SetFanSpeed_v2(0, 88))
	require.Equal(t, nvml.SUCCESS, dev.SetDefaultFanSpeed_v2(0))

	policy, ret := dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW), policy)

	speed, ret := dev.GetFanSpeed_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(41), speed, "the board's own reading is back")

	target, ret := dev.GetTargetFanSpeed(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 55, target, "and so is the profile's target")
}

func TestSetFanControlPolicy_RoundTrips(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, fannedConfig())

	require.Equal(t, nvml.SUCCESS, dev.SetFanControlPolicy(0, nvml.FAN_POLICY_MANUAL))
	policy, ret := dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_MANUAL), policy)

	// Switching to manual without naming a duty cycle must not invent one:
	// the fan keeps running at what it was doing until a speed is asked for.
	speed, ret := dev.GetFanSpeed_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(41), speed)

	require.Equal(t, nvml.SUCCESS, dev.SetFanControlPolicy(0, nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW))
	policy, ret = dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FanControlPolicy(nvml.FAN_POLICY_TEMPERATURE_CONTINOUS_SW), policy)
}

func TestSetFanControlPolicy_RejectsAnUnknownPolicy(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, fannedConfig())

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetFanControlPolicy(0, nvml.FanControlPolicy(7)),
		"NVML defines two policies; a third names nothing the driver could apply")
}

// TestFanSetters_DeclineWithoutAWriter covers a consumer that links the engine
// without the bridge that installs a writer. Reporting success for a duty cycle
// recorded nowhere is the failure this guards against.
func TestFanSetters_DeclineWithoutAWriter(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, fannedConfig())

	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.SetFanSpeed_v2(0, 50), "SetFanSpeed_v2")
	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.SetDefaultFanSpeed_v2(0), "SetDefaultFanSpeed_v2")
	require.Equal(t, nvml.ERROR_NO_PERMISSION,
		dev.SetFanControlPolicy(0, nvml.FAN_POLICY_MANUAL), "SetFanControlPolicy")
}

// TestFanFamily_LostDeviceReportsGpuIsLost keeps the family on the same
// failure-injection path as every other device export: once the driver has
// marked the GPU gone they must report the loss, not a plausible reading.
//
// No fan getter advances the failure counter — nvidia-smi reads fan speed once
// per -q run, so ticking here would make an "after_calls" scenario trip on a
// schedule that depends on how much output the caller asked for. The device is
// therefore tripped through a guarded call first.
func TestFanFamily_LostDeviceReportsGpuIsLost(t *testing.T) {
	t.Parallel()
	cfg := fannedConfig()
	cfg.Failure = &FailureInjectionConfig{Mode: FailureModeLost}
	dev := newTestDeviceWithConfig(t, cfg)

	_, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "expected the guarded call to trip the device")

	_, ret = dev.FanSpeedRPM(0)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "FanSpeedRPM")
	_, ret = dev.GetTargetFanSpeed(0)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetTargetFanSpeed")
	_, _, ret = dev.GetMinMaxFanSpeed()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetMinMaxFanSpeed")
	_, ret = dev.CoolerInfo(0)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "CoolerInfo")
	_, ret = dev.GetFanControlPolicy_v2(0)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, "GetFanControlPolicy_v2")
}
