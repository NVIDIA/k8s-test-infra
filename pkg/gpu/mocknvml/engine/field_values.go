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

	// Per-location ECC counters. NVML has no SRAM field id — SRAM counters are
	// only reachable through nvmlDeviceGetSramEccErrorStatus and
	// nvmlDeviceGetMemoryErrorCounter(NVML_MEMORY_LOCATION_SRAM).
	fiEccSbeVolL1  = 7
	fiEccDbeVolL1  = 8
	fiEccSbeVolL2  = 9
	fiEccDbeVolL2  = 10
	fiEccSbeVolDev = 11
	fiEccDbeVolDev = 12
	fiEccSbeVolReg = 13
	fiEccDbeVolReg = 14
	fiEccSbeVolTex = 15
	fiEccDbeVolTex = 16
	fiEccDbeVolCbu = 17
	fiEccSbeAggL1  = 18
	fiEccDbeAggL1  = 19
	fiEccSbeAggL2  = 20
	fiEccDbeAggL2  = 21
	fiEccSbeAggDev = 22
	fiEccDbeAggDev = 23
	fiEccSbeAggReg = 24
	fiEccDbeAggReg = 25
	fiEccSbeAggTex = 26
	fiEccDbeAggTex = 27
	fiEccDbeAggCbu = 28

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

	// GPU T.Limit temperature thresholds (Ada and later; these supersede
	// nvmlDeviceGetTemperatureThreshold). DCGM's cache manager reads them
	// through the field-value path, and NVSentinel's GpuThermalMarginWatch
	// needs the slowdown entry present to arm.
	fiTempShutdownTlimit = 193
	fiTempSlowdownTlimit = 194
	fiTempMemMaxTlimit   = 195
	fiTempGpuMaxTlimit   = 196
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
	// FieldValueInt maps to nvmlValue_t.siVal (NVML_VALUE_TYPE_SIGNED_INT).
	// The returned uint64 carries the value's low 32 bits in two's-complement
	// form (uint64(uint32(int32(v)))) so the bridge can reinterpret it as a
	// signed int; used by the T.Limit thresholds, whose offsets go negative.
	FieldValueInt
)

// eccLocationField describes which nvmlDeviceGetMemoryErrorCounter query one
// per-location ECC field id stands for.
type eccLocationField struct {
	errorType   nvml.MemoryErrorType
	counterType nvml.EccCounterType
	location    nvml.MemoryLocation
}

// eccLocationFields maps the per-location ECC field ids onto the counter each
// one names, so the field-value path DCGM reads and the dedicated getter
// nvidia-smi reads resolve from the same engine state.
var eccLocationFields = map[uint32]eccLocationField{
	fiEccSbeVolL1:  {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L1_CACHE},
	fiEccDbeVolL1:  {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L1_CACHE},
	fiEccSbeVolL2:  {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L2_CACHE},
	fiEccDbeVolL2:  {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_L2_CACHE},
	fiEccSbeVolDev: {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY},
	fiEccDbeVolDev: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY},
	fiEccSbeVolReg: {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE},
	fiEccDbeVolReg: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE},
	fiEccSbeVolTex: {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY},
	fiEccDbeVolTex: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY},
	fiEccDbeVolCbu: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_CBU},
	fiEccSbeAggL1:  {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L1_CACHE},
	fiEccDbeAggL1:  {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L1_CACHE},
	fiEccSbeAggL2:  {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L2_CACHE},
	fiEccDbeAggL2:  {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L2_CACHE},
	fiEccSbeAggDev: {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY},
	fiEccDbeAggDev: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DEVICE_MEMORY},
	fiEccSbeAggReg: {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE},
	fiEccDbeAggReg: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_REGISTER_FILE},
	fiEccSbeAggTex: {nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY},
	fiEccDbeAggTex: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_TEXTURE_MEMORY},
	fiEccDbeAggCbu: {nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_CBU},
}

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
//
//nolint:cyclop // existing complexity; refactor deferred
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

	case fiEccSbeVolL1, fiEccDbeVolL1, fiEccSbeVolL2, fiEccDbeVolL2,
		fiEccSbeVolDev, fiEccDbeVolDev, fiEccSbeVolReg, fiEccDbeVolReg,
		fiEccSbeVolTex, fiEccDbeVolTex, fiEccDbeVolCbu,
		fiEccSbeAggL1, fiEccDbeAggL1, fiEccSbeAggL2, fiEccDbeAggL2,
		fiEccSbeAggDev, fiEccDbeAggDev, fiEccSbeAggReg, fiEccDbeAggReg,
		fiEccSbeAggTex, fiEccDbeAggTex, fiEccDbeAggCbu:
		loc := eccLocationFields[fieldID]
		count, ret := d.GetMemoryErrorCounter(loc.errorType, loc.counterType, loc.location)
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

	case fiTempShutdownTlimit, fiTempSlowdownTlimit, fiTempMemMaxTlimit, fiTempGpuMaxTlimit:
		return d.tlimitThresholdFieldValue(fieldID)

	case fiPerfPolicyPower, fiPerfPolicyThermal, fiPerfPolicySyncBoost,
		fiClocksEventSwThermSlowdown, fiClocksEventHwThermSlowdown, fiClocksEventHwPowerBrake:
		return d.throttleCounterFieldValue(fieldID)

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

// tlimitThresholdFieldValue resolves the GPU T.Limit temperature threshold
// field values (NVML_FI_DEV_TEMPERATURE_*_TLIMIT, ids 193-196). Ada and later
// hardware reports these instead of the legacy nvmlDeviceGetTemperatureThreshold
// scalars, as the signed distance in degrees C from a common T.Limit reference
// to each threshold; nvidia-smi renders them as the "GPU/Memory <X> T.Limit
// Temp" rows and the live headroom (GetMarginTemperature / DCGM field 153) is
// measured against the same reference. Pre-Ada architectures return
// NOT_SUPPORTED so nvidia-smi falls back to the absolute threshold API. We use
// the slowdown threshold as that reference (matching GetMarginTemperature), so
// the slowdown offset is 0, the shutdown offset is negative (a hotter limit),
// and the GPU-max offset is the gap to the max-operating limit. NVSentinel's
// GpuThermalMarginWatch treats the slowdown entry as the metadata it needs to
// arm, then alarms as the live margin closes on it. The memory-max entry stays
// unsupported because the mock models no separate memory throttle threshold.
func (d *ConfigurableDevice) tlimitThresholdFieldValue(fieldID uint32) (FieldValueType, uint64, nvml.Return, bool) {
	if !d.reportsTLimit() {
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, true
	}
	c := d.cfg()
	if c.Thermal == nil {
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, true
	}
	reference := tlimitReference(c.Thermal)
	if reference == 0 {
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, true
	}
	var threshold int
	switch fieldID {
	case fiTempShutdownTlimit:
		threshold = c.Thermal.ShutdownThreshold_C
	case fiTempSlowdownTlimit:
		threshold = c.Thermal.SlowdownThreshold_C
	case fiTempGpuMaxTlimit:
		threshold = c.Thermal.MaxOperating_C
	default: // fiTempMemMaxTlimit — no memory throttle threshold modeled
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, true
	}
	if threshold == 0 {
		return FieldValueUnsupported, 0, nvml.ERROR_NOT_SUPPORTED, true
	}
	offset := int32(reference - threshold)
	return FieldValueInt, uint64(uint32(offset)), nvml.SUCCESS, true
}

func tlimitReference(thermal *ThermalConfig) int {
	if thermal.SlowdownThreshold_C != 0 {
		return thermal.SlowdownThreshold_C
	}
	if thermal.ShutdownThreshold_C != 0 {
		return thermal.ShutdownThreshold_C
	}
	return thermal.MaxOperating_C
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
