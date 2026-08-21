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

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestGetFieldValue_DeviceScope(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		ECC:          &ECCConfig{ModeCurrent: "enabled", ModePending: "enabled"},
		Thermal:      &ThermalConfig{TemperatureGPU_C: 34, TemperatureMemory_C: 42},
		Power:        &PowerConfig{CurrentDrawMW: 95000, TotalEnergyConsumptionMJ: 123456},
		RemappedRows: &RemappedRowsConfig{Correctable: 3, Uncorrectable: 1, Pending: true},
	})

	tests := []struct {
		name    string
		fieldID uint32
		vt      FieldValueType
		value   uint64
	}{
		{"ECC_CURRENT", fiEccCurrent, FieldValueUint, 1},
		{"ECC_PENDING", fiEccPending, FieldValueUint, 1},
		{"ECC_SBE_VOL_TOTAL", fiEccSbeVol, FieldValueUint64, 0},
		{"ECC_DBE_VOL_TOTAL", fiEccDbeVol, FieldValueUint64, 0},
		{"MEMORY_TEMP", fiMemoryTemp, FieldValueUint, 42},
		{"TOTAL_ENERGY", fiTotalEnergyConsumption, FieldValueUint64, 123456},
		{"PCIE_REPLAY", fiPcieReplayCounter, FieldValueUint, 0},
		{"PCIE_REPLAY_ROLLOVER", fiPcieReplayRolloverCounter, FieldValueUint, 0},
		{"RETIRED_PENDING_SBE", fiRetiredPendingSbe, FieldValueUint, 0},
		{"RETIRED_PENDING_DBE", fiRetiredPendingDbe, FieldValueUint, 0},
		{"REMAPPED_COR", fiRemappedCor, FieldValueUint, 3},
		{"REMAPPED_UNC", fiRemappedUnc, FieldValueUint, 1},
		{"REMAPPED_PENDING", fiRemappedPending, FieldValueUint, 1},
		{"REMAPPED_FAILURE", fiRemappedFailure, FieldValueUint, 0},
		{"RETIRED_SBE", fiRetiredSbe, FieldValueUint64, 0},
		{"RETIRED_PENDING", fiRetiredPending, FieldValueUint, 0},
	}
	for _, tt := range tests {
		vt, val, ret := dev.GetFieldValue(tt.fieldID, 0)
		require.Equal(t, nvml.SUCCESS, ret, "%s (field %d)", tt.name, tt.fieldID)
		require.Equal(t, tt.vt, vt, "%s value type", tt.name)
		require.Equal(t, tt.value, val, "%s value", tt.name)
	}
}

func TestGetFieldValue_PowerScope(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Power: &PowerConfig{
			CurrentDrawMW:   350000,
			EnforcedLimitMW: 600000,
			DefaultLimitMW:  700000,
			MinLimitMW:      100000,
			MaxLimitMW:      900000,
		},
	})

	tests := []struct {
		name    string
		fieldID uint32
		value   uint64
	}{
		{"POWER_AVERAGE", fiPowerAverage, 350000},
		{"POWER_INSTANT", fiPowerInstant, 350000},
		{"POWER_MIN_LIMIT", fiPowerMinLimit, 100000},
		{"POWER_MAX_LIMIT", fiPowerMaxLimit, 900000},
		{"POWER_DEFAULT_LIMIT", fiPowerDefaultLimit, 700000},
		{"POWER_CURRENT_LIMIT", fiPowerCurrentLimit, 600000},
		{"POWER_REQUESTED_LIMIT", fiPowerRequestedLimit, 600000},
	}
	for _, tt := range tests {
		// scopeId 0 == whole-GPU: resolves to the power getters.
		vt, val, ret := dev.GetFieldValue(tt.fieldID, 0)
		require.Equal(t, nvml.SUCCESS, ret, "%s (field %d)", tt.name, tt.fieldID)
		require.Equal(t, FieldValueUint, vt, "%s value type", tt.name)
		require.Equal(t, tt.value, val, "%s value", tt.name)

		// Non-zero scope (per-module / per-memory) is not modeled -> blank.
		_, _, ret = dev.GetFieldValue(tt.fieldID, 1)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "%s non-GPU scope", tt.name)
	}
}

func TestGetMarginTemperature(t *testing.T) {
	// The margin API is the T.Limit headroom, so it needs an Ada-or-later
	// architecture just like the T.Limit field IDs.
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Thermal:      &ThermalConfig{TemperatureGPU_C: 34, SlowdownThreshold_C: 87},
	})
	margin, ret := dev.GetMarginTemperature()
	require.Equal(t, nvml.SUCCESS, ret)
	// margin = slowdown (87) - current (34).
	require.Equal(t, int32(53), margin.MarginTemperature)

	// Current above the limit yields a NEGATIVE (signed) margin, mirroring real
	// T.Limit hardware: this is the sign crossing GpuThermalMarginWatch keys on.
	hot := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Thermal:      &ThermalConfig{TemperatureGPU_C: 120, SlowdownThreshold_C: 87},
	})
	margin, ret = hot.GetMarginTemperature()
	require.Equal(t, nvml.SUCCESS, ret)
	// margin = slowdown (87) - current (120) = -33.
	require.Equal(t, int32(-33), margin.MarginTemperature)

	// No thermal config -> not supported.
	none := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})
	_, ret = none.GetMarginTemperature()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestGetMarginTemperature_PreAdaNotSupported(t *testing.T) {
	// nvmlDeviceGetMarginTemperature reports headroom to the T.Limit
	// reference, which pre-Ada hardware does not have. While the mock answers
	// it, nvidia-smi keeps the whole temperature section in its "T.Limit" form
	// (the "GPU T.Limit Temp" row) and never prints the absolute
	// shutdown/slowdown/max-operating rows real Ampere and Turing report.
	for _, arch := range []string{"turing", "ampere"} {
		arch := arch
		t.Run(arch, func(t *testing.T) {
			dev := newTestDeviceWithConfig(t, &DeviceConfig{
				Architecture: arch,
				Thermal: &ThermalConfig{
					TemperatureGPU_C:    33,
					SlowdownThreshold_C: 87,
					ShutdownThreshold_C: 92,
					MaxOperating_C:      83,
				},
			})
			_, ret := dev.GetMarginTemperature()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret,
				"pre-Ada arch %q must not report a T.Limit margin", arch)

			// The absolute thresholds stay available: that is what nvidia-smi
			// falls back to on this hardware.
			shutdown, ret := dev.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_SHUTDOWN)
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, uint32(92), shutdown)
		})
	}
}

func TestGetFieldValue_TlimitThresholds(t *testing.T) {
	// T.Limit field IDs are Ada-and-later only. Declare an Ada+ architecture
	// so the supported path is exercised explicitly.
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Thermal: &ThermalConfig{
			TemperatureGPU_C:    34,
			SlowdownThreshold_C: 87,
			ShutdownThreshold_C: 92,
			MaxOperating_C:      85,
		},
	})

	// Offsets are signed distances from the slowdown reference (87): slowdown
	// is 0, shutdown a hotter (negative) limit, GPU-max the cooler gap. These
	// are the metadata NVSentinel's GpuThermalMarginWatch needs to arm and are
	// independent of the live temperature.
	cases := []struct {
		name    string
		fieldID uint32
		offset  int32
	}{
		{"SLOWDOWN_TLIMIT", fiTempSlowdownTlimit, 0},
		{"SHUTDOWN_TLIMIT", fiTempShutdownTlimit, -5},
		{"GPU_MAX_TLIMIT", fiTempGpuMaxTlimit, 2},
	}
	for _, tc := range cases {
		vt, val, ret := dev.GetFieldValue(tc.fieldID, 0)
		require.Equal(t, nvml.SUCCESS, ret, "%s (field %d)", tc.name, tc.fieldID)
		require.Equal(t, FieldValueInt, vt, "%s value type", tc.name)
		require.Equal(t, tc.offset, int32(uint32(val)), "%s offset", tc.name)
	}

	// The memory-max T.Limit entry is not modeled -> blank.
	_, _, ret := dev.GetFieldValue(fiTempMemMaxTlimit, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)

	// No thermal config at all -> every T.Limit entry is blank.
	none := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})
	_, _, ret = none.GetFieldValue(fiTempSlowdownTlimit, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestGetFieldValue_TlimitThresholds_PreAdaNotSupported(t *testing.T) {
	// Real Ampere/Turing hardware never reports the T.Limit field IDs;
	// nvidia-smi falls back to nvmlDeviceGetTemperatureThreshold absolute
	// scalars. The mock must return NOT_SUPPORTED so pre-Ada profiles do not
	// render signed margins as absolute (and inverted) temperatures.
	for _, arch := range []string{"turing", "ampere"} {
		arch := arch
		t.Run(arch, func(t *testing.T) {
			dev := newTestDeviceWithConfig(t, &DeviceConfig{
				Architecture: arch,
				Thermal: &ThermalConfig{
					TemperatureGPU_C:    33,
					SlowdownThreshold_C: 87,
					ShutdownThreshold_C: 92,
					MaxOperating_C:      83,
				},
			})
			for _, fieldID := range []uint32{
				fiTempShutdownTlimit,
				fiTempSlowdownTlimit,
				fiTempMemMaxTlimit,
				fiTempGpuMaxTlimit,
			} {
				_, _, ret := dev.GetFieldValue(fieldID, 0)
				require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret,
					"pre-Ada arch %q must not answer T.Limit field %d", arch, fieldID)
			}
		})
	}
}

func TestGetFieldValue_RetiredPages_BlackwellNotSupported(t *testing.T) {
	// The DCGM-facing retirement field IDs read the same NVML surface the
	// nvidia-smi Retired Pages block does, so the architecture gate has to
	// reach them too: a zero count here is the same fabricated answer.
	blackwell := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "blackwell"})
	for _, fieldID := range []uint32{
		fiRetiredSbe,
		fiRetiredDbe,
		fiRetiredPending,
		fiRetiredPendingSbe,
		fiRetiredPendingDbe,
	} {
		_, _, ret := blackwell.GetFieldValue(fieldID, 0)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret,
			"blackwell must not answer retirement field %d", fieldID)
	}

	ampere := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "ampere"})
	_, count, ret := ampere.GetFieldValue(fiRetiredDbe, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(0), count)
}

func TestGetFieldValue_UnknownFieldNotSupported(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "hopper"})
	vt, _, ret := dev.GetFieldValue(9999, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	require.Equal(t, FieldValueUnsupported, vt)
}

func TestGetFieldValue_MemoryTempUnset(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Thermal: &ThermalConfig{TemperatureGPU_C: 34},
	})
	_, _, ret := dev.GetFieldValue(fiMemoryTemp, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestGetFieldValue_EccUncorrectableInjection(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		ECC:          &ECCConfig{ModeCurrent: "enabled"},
		Failure: &FailureInjectionConfig{
			Mode: FailureModeECCUncorrectable,
		},
	})

	// The injected uncorrectable-ECC counter is a strictly increasing call
	// counter; two consecutive reads must both be nonzero and rising.
	_, v1, ret := dev.GetFieldValue(fiEccDbeVol, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	_, v2, ret := dev.GetFieldValue(fiEccDbeVol, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Greater(t, v2, v1, "DBE counter must increase under injection")

	// Remapped-rows failure flag must also trip.
	_, failure, ret := dev.GetFieldValue(fiRemappedFailure, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(1), failure)
}
