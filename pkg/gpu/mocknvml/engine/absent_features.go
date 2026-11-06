// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// absentFeatureMinDriver is the first driver on which a feature the profile
// leaves out answers NOT_SUPPORTED, as a real board without it does. It applies
// to any profile run on such a driver: gb300 with driverVersion 615.71.09
// answers DRAM encryption NOT_SUPPORTED, since it has no ecc.dram_encryption
// block. Real boards on older drivers answer the same way, but the profiles for
// them were written against the mock's defaults (GPU operation mode, retired
// pages, DRAM encryption, acoustic thresholds), so drivers before 615, the
// first branch checked against a real node, keep those defaults until those
// profiles are aligned with their captures.
const absentFeatureMinDriver = "615"

// configuredDriver is the driver the mock reports: the chart's driverVersion,
// else the profile's system.driver_version.
func configuredDriver(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	if cfg.DriverVersion != "" {
		return cfg.DriverVersion
	}
	if cfg.YAMLConfig != nil {
		return cfg.YAMLConfig.System.DriverVersion
	}
	return ""
}

// absentIsUnsupported reports whether a feature the profile leaves out answers
// NOT_SUPPORTED rather than the mock's default.
func (d *ConfigurableDevice) absentIsUnsupported() bool {
	return d.driverVersion != "" && CompareDriverVersions(d.driverVersion, absentFeatureMinDriver) >= 0
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
// driver answers absent features that way, SUCCESS otherwise.
func (d *ConfigurableDevice) unsupportedWhenAbsent(call string, declared bool) nvml.Return {
	if declared || !d.absentIsUnsupported() {
		return nvml.SUCCESS
	}
	debugLog("[NVML] %s -> NOT_SUPPORTED (not in the profile, driver %s)\n", call, d.driverVersion)
	return nvml.ERROR_NOT_SUPPORTED
}
