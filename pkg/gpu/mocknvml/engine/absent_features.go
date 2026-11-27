// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// The values of system.absent_features: what a feature the profile leaves
// out answers.
//
// AbsentFeaturesNotSupported answers NOT_SUPPORTED (nvidia-smi shows N/A), as
// a real GPU without the feature does. A profile checked against a capture of
// real hardware sets it, so leaving a feature out is a statement about the
// board.
//
// AbsentFeaturesLegacyDefaults, the default, answers with the mock's built-in
// values, as if the feature existed. The profiles written before captures were
// available rely on those values, so they keep them until each is checked
// against its capture.
//
// The setting belongs to the profile, not the driver: the driver version
// decides which calls exist and how nvidia-smi renders them, not how fully the
// profile describes the GPU.
const (
	AbsentFeaturesLegacyDefaults = "legacy_defaults"
	AbsentFeaturesNotSupported   = "not_supported"
)

// absentFeaturesNotSupported reports whether the profile sets
// absent_features: not_supported. Any other value keeps the legacy defaults,
// and an unknown one is warned about rather than refused.
func absentFeaturesNotSupported(cfg *Config) bool {
	if cfg == nil || cfg.YAMLConfig == nil {
		return false
	}
	switch v := cfg.YAMLConfig.System.AbsentFeatures; v {
	case AbsentFeaturesNotSupported:
		return true
	case "", AbsentFeaturesLegacyDefaults:
		return false
	default:
		warnLog("[CONFIG] system.absent_features=%q is not %q or %q; using %q\n",
			v, AbsentFeaturesLegacyDefaults, AbsentFeaturesNotSupported, AbsentFeaturesLegacyDefaults)
		return false
	}
}

// declaresThreshold reports whether the profile declares a temperature
// threshold beyond shutdown, slowdown and GPU max, by the key for the same
// quantity: the memory maximum by a memory temperature, the acoustic range by a
// target temperature (nvidia-smi's GPU Target Temperature and its range read
// it). The GPS threshold has no key, so a profile never declares it.
func declaresThreshold(thermal *ThermalConfig, threshold nvml.TemperatureThresholds) bool {
	switch threshold {
	case nvml.TEMPERATURE_THRESHOLD_MEM_MAX:
		return thermal.TemperatureMemory_C != 0
	case nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_MIN, nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR, nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_MAX:
		return thermal.TargetTemperature_C != 0
	default:
		return false
	}
}

// unsupportedWhenAbsent returns NOT_SUPPORTED when declared is false and the
// profile sets absent_features: not_supported, SUCCESS otherwise.
func (d *ConfigurableDevice) unsupportedWhenAbsent(call string, declared bool) nvml.Return {
	if declared || !d.absentNotSupported {
		return nvml.SUCCESS
	}
	debugLog("[NVML] %s -> NOT_SUPPORTED (not in the profile)\n", call)
	return nvml.ERROR_NOT_SUPPORTED
}
