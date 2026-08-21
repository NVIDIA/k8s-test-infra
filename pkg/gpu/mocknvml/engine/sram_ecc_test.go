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

// eccEnabled is the ECC block every SRAM scenario starts from: SRAM ECC
// reporting is a property of an ECC-enabled GPU, so the mode drives whether the
// status is answerable at all.
func eccEnabled() *ECCConfig {
	return &ECCConfig{ModeCurrent: "enabled", ModePending: "enabled"}
}

func TestSramEccErrorStatus_HealthyDeviceReportsZeroNotUnsupported(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{ECC: eccEnabled()})

	status, ret := dev.GetSramEccErrorStatus()
	require.Equal(t, nvml.SUCCESS, ret,
		"an ECC-enabled GPU must answer the SRAM status so nvidia-smi renders 0 rather than N/A")
	require.Equal(t, nvml.EccSramErrorStatus{}, status, "a healthy GPU reports every SRAM counter as zero")
}

func TestSramEccErrorStatus_UnsupportedWhenEccDisabled(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		ECC: &ECCConfig{ModeCurrent: "disabled", ModePending: "disabled"},
	})

	_, ret := dev.GetSramEccErrorStatus()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret,
		"a GPU with ECC off has no SRAM error state to report")
}

func TestSramEccErrorStatus_ReportsConfiguredCounters(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		ECC: &ECCConfig{
			ModeCurrent: "enabled",
			ModePending: "enabled",
			SRAM: &ECCSramConfig{
				Volatile: &ECCSramCountsConfig{
					Correctable:         7,
					UncorrectableParity: 2,
					UncorrectableSECDED: 3,
				},
				Aggregate: &ECCSramCountsConfig{
					Correctable:         11,
					UncorrectableParity: 5,
					UncorrectableSECDED: 6,
				},
				UncorrectableSources: &ECCSramSourcesConfig{
					L2:              4,
					SM:              3,
					Microcontroller: 2,
					PCIe:            1,
					Other:           1,
				},
				ThresholdExceeded: true,
			},
		},
	})

	status, ret := dev.GetSramEccErrorStatus()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.EccSramErrorStatus{
		VolatileCor:             7,
		VolatileUncParity:       2,
		VolatileUncSecDed:       3,
		AggregateCor:            11,
		AggregateUncParity:      5,
		AggregateUncSecDed:      6,
		AggregateUncBucketL2:    4,
		AggregateUncBucketSm:    3,
		AggregateUncBucketMcu:   2,
		AggregateUncBucketPcie:  1,
		AggregateUncBucketOther: 1,
		BThresholdExceeded:      1,
	}, status)
}

func TestSramEccErrorStatus_LostDeviceReportsGpuIsLost(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		ECC:     eccEnabled(),
		Failure: &FailureInjectionConfig{Mode: FailureModeLost},
	})

	_, ret := dev.GetSramEccErrorStatus()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret,
		"a lost GPU must fail the SRAM query like every other guarded getter")
}

// TestMemoryErrorCounter_DistinguishesDramFromSram is the locationType half of
// the fix: one shared counter for every location made the argument meaningless.
func TestMemoryErrorCounter_DistinguishesDramFromSram(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		ECC: &ECCConfig{
			ModeCurrent: "enabled",
			ModePending: "enabled",
			Errors: &ECCErrorsConfig{
				Aggregate: &ECCErrorCountsConfig{
					DoubleBit: &ECCMemoryErrorsConfig{DeviceMemory: 9, L2Cache: 4},
				},
			},
			SRAM: &ECCSramConfig{
				Aggregate: &ECCSramCountsConfig{UncorrectableParity: 2, UncorrectableSECDED: 3},
			},
		},
	})

	dram, ret := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DRAM)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(9), dram, "DRAM reads the device-memory counter")

	sram, ret := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_SRAM)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(5), sram, "SRAM uncorrectable is parity + SEC-DED, and must not read the DRAM counter")

	l2, ret := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_L2_CACHE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(4), l2, "each location reports its own configured counter")
}

func TestMemoryErrorCounter_DistinguishesVolatileFromAggregate(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		ECC: &ECCConfig{
			ModeCurrent: "enabled",
			ModePending: "enabled",
			Errors: &ECCErrorsConfig{
				Volatile:  &ECCErrorCountsConfig{SingleBit: &ECCMemoryErrorsConfig{DeviceMemory: 1}},
				Aggregate: &ECCErrorCountsConfig{SingleBit: &ECCMemoryErrorsConfig{DeviceMemory: 12}},
			},
			SRAM: &ECCSramConfig{
				Volatile:  &ECCSramCountsConfig{Correctable: 2},
				Aggregate: &ECCSramCountsConfig{Correctable: 20},
			},
		},
	})

	volatileDRAM, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_DRAM)
	aggregateDRAM, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_DRAM)
	require.Equal(t, uint64(1), volatileDRAM)
	require.Equal(t, uint64(12), aggregateDRAM)

	volatileSRAM, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.VOLATILE_ECC, nvml.MEMORY_LOCATION_SRAM)
	aggregateSRAM, _ := dev.GetMemoryErrorCounter(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC, nvml.MEMORY_LOCATION_SRAM)
	require.Equal(t, uint64(2), volatileSRAM)
	require.Equal(t, uint64(20), aggregateSRAM)
}

func TestTotalEccErrors_ReadsConfiguredTotalsPerCounterType(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		ECC: &ECCConfig{
			ModeCurrent: "enabled",
			ModePending: "enabled",
			Errors: &ECCErrorsConfig{
				Volatile:  &ECCErrorCountsConfig{DoubleBit: &ECCMemoryErrorsConfig{Total: 3}},
				Aggregate: &ECCErrorCountsConfig{DoubleBit: &ECCMemoryErrorsConfig{Total: 30}},
			},
		},
	})

	vol, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC)
	agg, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC)
	require.Equal(t, uint64(3), vol)
	require.Equal(t, uint64(30), agg)

	cor, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_CORRECTED, nvml.AGGREGATE_ECC)
	require.Zero(t, cor, "no single-bit totals configured")
}

func TestRowRemapperHistogram_UnsupportedWhenUnconfigured(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		RemappedRows: &RemappedRowsConfig{Correctable: 1},
	})

	_, ret := dev.GetRowRemapperHistogram()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret,
		"bank availability is only knowable from config; pre-Ampere GPUs report nothing")
}

func TestRowRemapperHistogram_ReportsConfiguredBanks(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		RemappedRows: &RemappedRowsConfig{
			Uncorrectable: 2,
			AvailabilityHistogram: &RowRemapHistogramConfig{
				Max: 636, High: 2, Partial: 1, Low: 1, None: 0,
			},
		},
	})

	histogram, ret := dev.GetRowRemapperHistogram()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.RowRemapperHistogramValues{Max: 636, High: 2, Partial: 1, Low: 1}, histogram)
}

func TestRowRemapperHistogram_LostDeviceReportsGpuIsLost(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		RemappedRows: &RemappedRowsConfig{AvailabilityHistogram: &RowRemapHistogramConfig{Max: 640}},
		Failure:      &FailureInjectionConfig{Mode: FailureModeLost},
	})

	_, ret := dev.GetRowRemapperHistogram()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
}

// TestFieldValues_PerLocationEccErrors pins the DCGM query path to the same
// engine state as the getters: DCGM reads per-location ECC counters through
// nvmlDeviceGetFieldValues, so a value only reachable through nvidia-smi would
// leave its dashboards blank.
func TestFieldValues_PerLocationEccErrors(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		ECC: &ECCConfig{
			ModeCurrent: "enabled",
			ModePending: "enabled",
			Errors: &ECCErrorsConfig{
				Volatile: &ECCErrorCountsConfig{
					SingleBit: &ECCMemoryErrorsConfig{L1Cache: 1, L2Cache: 2, DeviceMemory: 3},
					DoubleBit: &ECCMemoryErrorsConfig{RegisterFile: 4, TextureMemory: 5},
				},
				Aggregate: &ECCErrorCountsConfig{
					SingleBit: &ECCMemoryErrorsConfig{L2Cache: 20},
					DoubleBit: &ECCMemoryErrorsConfig{DeviceMemory: 30},
				},
			},
		},
	})

	for _, tc := range []struct {
		name    string
		fieldID uint32
		want    uint64
	}{
		{"volatile single-bit L1", fiEccSbeVolL1, 1},
		{"volatile single-bit L2", fiEccSbeVolL2, 2},
		{"volatile single-bit device memory", fiEccSbeVolDev, 3},
		{"volatile double-bit register file", fiEccDbeVolReg, 4},
		{"volatile double-bit texture memory", fiEccDbeVolTex, 5},
		{"aggregate single-bit L2", fiEccSbeAggL2, 20},
		{"aggregate double-bit device memory", fiEccDbeAggDev, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vt, val, ret := dev.GetFieldValue(tc.fieldID, 0)
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, FieldValueUint64, vt)
			require.Equal(t, tc.want, val)
		})
	}
}
