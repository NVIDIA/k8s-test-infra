// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// nvlinkFieldTelemetryMinDriver is the first driver that reports NVLink
// telemetry through field values only. A real 615 tray answers NOT_SUPPORTED
// to the per-link error and utilization counter getters, for every link and
// counter, and to the per-link speed and data-link error fields. It reports the
// counts through the NVLink5 counter fields (NVML_FI_DEV_NVLINK_COUNT_*), which
// `nvidia-smi nvlink -e` reads and an NVLinkError injection raises. Older
// drivers keep answering the getters.
const nvlinkFieldTelemetryMinDriver = "615"

// nvlinkTelemetryIsFieldsOnly reports whether the configured driver serves
// NVLink telemetry through field values only.
func (d *ConfigurableDevice) nvlinkTelemetryIsFieldsOnly() bool {
	return d.driverVersion != "" && CompareDriverVersions(d.driverVersion, nvlinkFieldTelemetryMinDriver) >= 0
}

// isLegacyNVLinkField reports whether a field id is one of the per-link speed
// or data-link error fields a fields-only driver no longer answers.
func isLegacyNVLinkField(fieldID uint32) bool {
	if _, ok := speedFieldLink(fieldID); ok {
		return true
	}
	switch fieldID {
	case fiNvlinkSpeedMbpsCommon, fiNvlinkErrorDlReplay, fiNvlinkErrorDlRecovery, fiNvlinkErrorDlCrc:
		return true
	default:
		return false
	}
}

// legacyNVLinkTelemetry returns NOT_SUPPORTED for a legacy NVLink counter call
// on a fields-only driver, SUCCESS otherwise.
func (d *ConfigurableDevice) legacyNVLinkTelemetry(call string) nvml.Return {
	if !d.nvlinkTelemetryIsFieldsOnly() {
		return nvml.SUCCESS
	}
	debugLog("[NVML] %s -> NOT_SUPPORTED (field values only on driver %s)\n", call, d.driverVersion)
	return nvml.ERROR_NOT_SUPPORTED
}
