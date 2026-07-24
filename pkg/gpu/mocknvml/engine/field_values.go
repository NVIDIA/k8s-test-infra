// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Device-scope nvmlDeviceGetFieldValues dispatch. DCGM's cache manager reads
// most of its non-profiling telemetry (ECC totals, remapped rows, memory
// temperature, energy, PCIe replay) through field values rather than the
// dedicated getters, treating per-field NOT_SUPPORTED as a blank value. The
// values here are resolved from the same engine state as the corresponding
// getters so both query paths stay consistent.

package engine

import (
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// NVML field IDs (nvmlFieldValue_t.fieldId) for the device scope, mirrored
// from vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.h (NVML_FI_DEV_*).
const (
	fiEccCurrent = 1
	fiEccPending = 2
	fiEccSbeVol  = 3
	fiEccDbeVol  = 4
	fiEccSbeAgg  = 5
	fiEccDbeAgg  = 6

	fiRetiredSbe     = 29
	fiRetiredDbe     = 30
	fiRetiredPending = 31

	fiMemoryTemp             = 82
	fiTotalEnergyConsumption = 83

	fiRetiredPendingSbe = 92
	fiRetiredPendingDbe = 93

	fiPcieReplayCounter         = 94
	fiPcieReplayRolloverCounter = 95

	fiPowerAverage        = 185
	fiPowerInstant        = 186
	fiPowerMinLimit       = 187
	fiPowerMaxLimit       = 188
	fiPowerDefaultLimit   = 189
	fiPowerCurrentLimit   = 190
	fiPowerRequestedLimit = 192

	fiRemappedCor     = 142
	fiRemappedUnc     = 143
	fiRemappedPending = 144
	fiRemappedFailure = 145
)

// FieldValueType identifies which nvmlValue_t union member the bridge must
// populate for a resolved field value.
type FieldValueType uint8

const (
	// FieldValueUnsupported means the field id is not modeled; the bridge
	// should set the per-field nvmlReturn to NOT_SUPPORTED.
	FieldValueUnsupported FieldValueType = iota
	// FieldValueUint maps to nvmlValue_t.uiVal (NVML_VALUE_TYPE_UNSIGNED_INT).
	FieldValueUint
	// FieldValueUint64 maps to nvmlValue_t.ullVal
	// (NVML_VALUE_TYPE_UNSIGNED_LONG_LONG).
	FieldValueUint64
	// FieldValueDouble maps to nvmlValue_t.dVal (NVML_VALUE_TYPE_DOUBLE); the
	// returned value carries math.Float64bits of the double (the bridge writes
	// the raw 8 bytes into the shared union).
	FieldValueDouble
)

// GetFieldValue resolves a single nvmlDeviceGetFieldValues entry: device-scope
// fields first, then the NVLink field set. Unmodeled field ids yield
// (FieldValueUnsupported, 0, ERROR_NOT_SUPPORTED) so the bridge can mark just
// that entry unsupported while succeeding the overall call (matching real NVML
// semantics — DCGM renders such entries as blank, not as errors).
func (d *ConfigurableDevice) GetFieldValue(fieldID, scopeID uint32) (FieldValueType, uint64, nvml.Return) {
	if vt, val, ret, handled := d.getDeviceFieldValue(fieldID, scopeID); handled {
		return vt, val, ret
	}
	return d.GetNvLinkFieldValue(fieldID, scopeID)
}

// boolField converts a bool into the 1/0 encoding NVML uses for flag fields.
func boolField(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// getDeviceFieldValue resolves the device-scope (non-NVLink) field set. The
// fourth return reports whether the field id belongs to this set at all;
// unknown ids fall through to the NVLink dispatch in GetFieldValue.
func (d *ConfigurableDevice) getDeviceFieldValue(fieldID, scopeID uint32) (FieldValueType, uint64, nvml.Return, bool) {
	switch fieldID {
	case fiEccCurrent, fiEccPending:
		current, pending, ret := d.GetEccMode()
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret, true
		}
		state := current
		if fieldID == fiEccPending {
			state = pending
		}
		return FieldValueUint, boolField(state == nvml.FEATURE_ENABLED), nvml.SUCCESS, true

	case fiEccSbeVol, fiEccDbeVol, fiEccSbeAgg, fiEccDbeAgg:
		errorType := nvml.MEMORY_ERROR_TYPE_CORRECTED
		if fieldID == fiEccDbeVol || fieldID == fiEccDbeAgg {
			errorType = nvml.MEMORY_ERROR_TYPE_UNCORRECTED
		}
		counterType := nvml.VOLATILE_ECC
		if fieldID == fiEccSbeAgg || fieldID == fiEccDbeAgg {
			counterType = nvml.AGGREGATE_ECC
		}
		count, ret := d.GetTotalEccErrors(errorType, counterType)
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret, true
		}
		return FieldValueUint64, count, nvml.SUCCESS, true

	case fiRetiredSbe, fiRetiredDbe:
		cause := nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS
		if fieldID == fiRetiredDbe {
			cause = nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR
		}
		pages, ret := d.GetRetiredPages(cause)
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret, true
		}
		return FieldValueUint64, uint64(len(pages)), nvml.SUCCESS, true

	case fiRetiredPending, fiRetiredPendingSbe, fiRetiredPendingDbe:
		state, ret := d.GetRetiredPagesPendingStatus()
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret, true
		}
		return FieldValueUint, boolField(state == nvml.FEATURE_ENABLED), nvml.SUCCESS, true

	case fiMemoryTemp:
		cfg := d.cfg()
		if cfg.Thermal == nil || cfg.Thermal.TemperatureMemory_C == 0 {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, true
		}
		return FieldValueUint, uint64(cfg.Thermal.TemperatureMemory_C), nvml.SUCCESS, true

	case fiTotalEnergyConsumption:
		energy, ret := d.GetTotalEnergyConsumption()
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret, true
		}
		return FieldValueUint64, energy, nvml.SUCCESS, true

	case fiPcieReplayCounter, fiPcieReplayRolloverCounter:
		if fieldID == fiPcieReplayRolloverCounter {
			return FieldValueUint, 0, nvml.SUCCESS, true
		}
		count, ret := d.GetPcieReplayCounter()
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret, true
		}
		return FieldValueUint, uint64(count), nvml.SUCCESS, true

	case fiPowerAverage, fiPowerInstant, fiPowerMinLimit, fiPowerMaxLimit,
		fiPowerDefaultLimit, fiPowerCurrentLimit, fiPowerRequestedLimit:
		// Power field values are modeled only for the whole-GPU scope (scopeId
		// 0). Per-module / per-memory scopes (used by nvidia-smi -q's "Module"
		// and "GPU Memory" power sections) aren't simulated, so leave them
		// blank rather than fabricating a per-scope reading.
		if scopeID != 0 {
			return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, true
		}
		return d.powerFieldValue(fieldID)

	case fiRemappedCor, fiRemappedUnc, fiRemappedPending, fiRemappedFailure:
		corr, unc, pending, failure, ret := d.GetRemappedRows()
		if ret != nvml.SUCCESS {
			return FieldValueUnsupported, 0, ret, true
		}
		switch fieldID {
		case fiRemappedCor:
			return FieldValueUint, uint64(corr), nvml.SUCCESS, true
		case fiRemappedUnc:
			return FieldValueUint, uint64(unc), nvml.SUCCESS, true
		case fiRemappedPending:
			return FieldValueUint, boolField(pending), nvml.SUCCESS, true
		default:
			return FieldValueUint, boolField(failure), nvml.SUCCESS, true
		}

	default:
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, false
	}
}

// powerFieldValue resolves the whole-GPU power field values (mW) from the same
// getters as the dedicated power APIs, so the field-value path (used by
// nvidia-smi's power.draw.instant / -q power readings and by DCGM) stays
// consistent with nvmlDeviceGetPowerUsage & friends. The mock has no separate
// "instantaneous" sensor, so average and instant return the same draw.
func (d *ConfigurableDevice) powerFieldValue(fieldID uint32) (FieldValueType, uint64, nvml.Return, bool) {
	var (
		val uint32
		ret nvml.Return
	)
	switch fieldID {
	case fiPowerAverage, fiPowerInstant:
		val, ret = d.GetPowerUsage()
	case fiPowerMinLimit:
		val, _, ret = d.GetPowerManagementLimitConstraints()
	case fiPowerMaxLimit:
		_, val, ret = d.GetPowerManagementLimitConstraints()
	case fiPowerDefaultLimit:
		val, ret = d.GetPowerManagementDefaultLimit()
	case fiPowerCurrentLimit, fiPowerRequestedLimit:
		val, ret = d.GetPowerManagementLimit()
	}
	if ret != nvml.SUCCESS {
		return FieldValueUnsupported, 0, ret, true
	}
	return FieldValueUint, uint64(val), nvml.SUCCESS, true
}
