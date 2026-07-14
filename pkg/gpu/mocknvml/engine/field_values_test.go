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
