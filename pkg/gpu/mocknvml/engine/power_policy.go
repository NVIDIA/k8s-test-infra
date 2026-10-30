// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

// GetPowerSource reports AC: every board Mokka models is a datacenter part on
// mains power, so neither battery nor an undersized supply is reachable.
func (d *ConfigurableDevice) GetPowerSource() (nvml.PowerSource, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return 0, ret
	}
	debugLog("[NVML] nvmlDeviceGetPowerSource -> AC\n")
	return nvml.POWER_SOURCE_AC, nvml.SUCCESS
}

// powerMizerModeNames maps the config spelling of each NVML PowerMizer mode to
// its NVML_POWER_MIZER_MODE_* value.
var powerMizerModeNames = map[string]uint32{
	"adaptive":                      nvml.POWER_MIZER_MODE_ADAPTIVE,
	"prefer_maximum_performance":    nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE,
	"auto":                          nvml.POWER_MIZER_MODE_AUTO,
	"prefer_consistent_performance": nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE,
}

// supportedPowerMizerModes is every mode NVML defines. Nothing in the hardware
// captures narrows it, so the mock accepts all four.
const supportedPowerMizerModes = 1<<nvml.POWER_MIZER_MODE_ADAPTIVE |
	1<<nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE |
	1<<nvml.POWER_MIZER_MODE_AUTO |
	1<<nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE

// powerMizerSupported applies NVML's "Maxwell or newer" gate.
func (d *ConfigurableDevice) powerMizerSupported() bool {
	return gpuarch.Arch(d.Config.Architecture).AtLeast(gpuarch.Maxwell)
}

// GetPowerMizerMode_v1 reports the current PowerMizer mode and the modes the
// device accepts.
func (d *ConfigurableDevice) GetPowerMizerMode_v1() (nvml.DevicePowerMizerModes_v1, nvml.Return) {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return nvml.DevicePowerMizerModes_v1{}, ret
	}
	if !d.powerMizerSupported() {
		debugLog("[NVML] nvmlDeviceGetPowerMizerMode_v1 -> NOT_SUPPORTED (pre-Maxwell)\n")
		return nvml.DevicePowerMizerModes_v1{}, nvml.ERROR_NOT_SUPPORTED
	}
	mode := d.powerMizerMode()
	debugLog("[NVML] nvmlDeviceGetPowerMizerMode_v1 -> %d\n", mode)
	return nvml.DevicePowerMizerModes_v1{
		CurrentMode:              mode,
		SupportedPowerMizerModes: supportedPowerMizerModes,
	}, nvml.SUCCESS
}

// SetPowerMizerMode_v1 applies modes.Mode. Like the power limit it is recorded
// in the override document, so a separate process reads the new mode back.
func (d *ConfigurableDevice) SetPowerMizerMode_v1(modes *nvml.DevicePowerMizerModes_v1) nvml.Return {
	if modes == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		debugLog("[NVML] nvmlDeviceSetPowerMizerMode_v1(%d) -> %d (injected failure)\n", modes.Mode, ret)
		return ret
	}
	if !d.powerMizerSupported() {
		debugLog("[NVML] nvmlDeviceSetPowerMizerMode_v1(%d) -> NOT_SUPPORTED (pre-Maxwell)\n", modes.Mode)
		return nvml.ERROR_NOT_SUPPORTED
	}
	name, ok := powerMizerModeName(modes.Mode)
	if !ok {
		debugLog("[NVML] nvmlDeviceSetPowerMizerMode_v1(%d) -> INVALID_ARGUMENT\n", modes.Mode)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] nvmlDeviceSetPowerMizerMode_v1(%d) -> NO_PERMISSION (no override writer)\n", modes.Mode)
		return nvml.ERROR_NO_PERMISSION
	}
	if err := w.SetPowerMizerMode(d.PhysicalIndex(), name); err != nil {
		warnLog("[NVML] nvmlDeviceSetPowerMizerMode_v1(%d) -> NO_PERMISSION: %v\n", modes.Mode, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] nvmlDeviceSetPowerMizerMode_v1(%s)\n", name)
	return nvml.SUCCESS
}

// powerMizerMode resolves the configured mode, falling back to the
// driver-controlled AUTO for an empty or unknown name.
func (d *ConfigurableDevice) powerMizerMode() uint32 {
	c := d.cfg()
	if c.Power == nil || c.Power.PowerMizerMode == "" {
		return nvml.POWER_MIZER_MODE_AUTO
	}
	mode, ok := powerMizerModeNames[c.Power.PowerMizerMode]
	if !ok {
		debugLog("[NVML] unknown power_mizer_mode %q, reporting auto\n", c.Power.PowerMizerMode)
		return nvml.POWER_MIZER_MODE_AUTO
	}
	return mode
}

func powerMizerModeName(mode uint32) (string, bool) {
	for name, v := range powerMizerModeNames {
		if v == mode {
			return name, true
		}
	}
	return "", false
}
