// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// absentFeatureMinDriver is the first driver on which a feature the profile
// leaves out answers NOT_SUPPORTED, as a real board without it does. Real
// boards on older drivers answer the same way, but the profiles for them were
// written against the mock's defaults (GPU operation mode, retired pages, DRAM
// encryption, acoustic thresholds), so drivers before 615, the first branch
// checked against a real node, keep those defaults.
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

// unsupportedWhenAbsent returns NOT_SUPPORTED when declared is false and the
// driver answers absent features that way, SUCCESS otherwise.
func (d *ConfigurableDevice) unsupportedWhenAbsent(call string, declared bool) nvml.Return {
	if declared || !d.absentIsUnsupported() {
		return nvml.SUCCESS
	}
	debugLog("[NVML] %s -> NOT_SUPPORTED (not in the profile, driver %s)\n", call, d.driverVersion)
	return nvml.ERROR_NOT_SUPPORTED
}
