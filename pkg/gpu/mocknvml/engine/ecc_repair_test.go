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

func repairConfig(arch string, rows *RemappedRowsConfig, ecc *ECCConfig) *DeviceConfig {
	if ecc == nil {
		ecc = eccEnabled()
	}
	return &DeviceConfig{Architecture: arch, ECC: ecc, RemappedRows: rows}
}

func eccUncorrectableFailure() *FailureInjectionConfig {
	return &FailureInjectionConfig{Mode: "ecc_uncorrectable", AfterCalls: 1}
}

func TestRepairStatus_UnsupportedBeforeAmpere(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, repairConfig("turing", nil, nil))

	_, ret := dev.GetRepairStatus()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestRepairStatus_HealthyDeviceHasNothingPending(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, repairConfig("hopper", &RemappedRowsConfig{Correctable: 2}, nil))

	status, ret := dev.GetRepairStatus()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, status.BChannelRepairPending)
	require.Zero(t, status.BTpcRepairPending)
}

func TestRepairStatus_PendingRowRemapIsAPendingChannelRepair(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, repairConfig("hopper", &RemappedRowsConfig{Pending: true}, nil))

	status, ret := dev.GetRepairStatus()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), status.BChannelRepairPending)
}

// TestInjectedUncorrectableFault_MovesRepairStatusAndUnrepairableFlagTogether
// is the remediation loop the getters exist for: one injected fault must show
// up in the remapper, the repair status and the unrepairable flag alike, or a
// health agent reads a contradictory picture.
func TestInjectedUncorrectableFault_MovesRepairStatusAndUnrepairableFlagTogether(t *testing.T) {
	t.Parallel()
	cfg := repairConfig("hopper", &RemappedRowsConfig{}, nil)
	cfg.Failure = eccUncorrectableFailure()
	dev := newTestDeviceWithConfig(t, cfg)

	// Trip the injector the way a poll does.
	_, _ = dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC)

	status, ret := dev.GetRepairStatus()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), status.BChannelRepairPending)

	flag, ret := dev.GetUnrepairableMemoryFlag()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), flag.BUnrepairableMemory)
}

func TestUnrepairableMemoryFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  *DeviceConfig
		ret  nvml.Return
		want uint32
	}{
		{"unsupported before Hopper", repairConfig("ampere", nil, nil), nvml.ERROR_NOT_SUPPORTED, 0},
		{"healthy", repairConfig("hopper", &RemappedRowsConfig{Correctable: 4}, nil), nvml.SUCCESS, 0},
		{"remap failure", repairConfig("hopper", &RemappedRowsConfig{FailureOccurred: true}, nil), nvml.SUCCESS, 1},
		{
			"SRAM threshold exceeded",
			repairConfig("hopper", nil, &ECCConfig{
				ModeCurrent: "enabled",
				SRAM:        &ECCSramConfig{ThresholdExceeded: true},
			}),
			nvml.SUCCESS, 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, tt.cfg)

			flag, ret := dev.GetUnrepairableMemoryFlag()
			require.Equal(t, tt.ret, ret)
			require.Equal(t, tt.want, flag.BUnrepairableMemory)
		})
	}
}

func sramSourcesConfig() *DeviceConfig {
	return repairConfig("blackwell", nil, &ECCConfig{
		ModeCurrent: "enabled",
		SRAM: &ECCSramConfig{UncorrectableSources: &ECCSramSourcesConfig{
			L2: 3, SM: 0, Microcontroller: 5, PCIe: 0, Other: 1,
		}},
	})
}

func TestSramUniqueUncorrectedCounts_SizeProbeThenFill(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, sramSourcesConfig())

	probe := nvml.EccSramUniqueUncorrectedErrorCounts{}
	require.Equal(t, nvml.SUCCESS, dev.GetSramUniqueUncorrectedEccErrorCounts(&probe))
	require.Equal(t, uint32(3), probe.EntryCount, "one entry per source that reported errors")

	entries := make([]nvml.EccSramUniqueUncorrectedErrorEntry_v1, probe.EntryCount)
	fill := nvml.EccSramUniqueUncorrectedErrorCounts{EntryCount: probe.EntryCount, Entries: &entries[0]}
	require.Equal(t, nvml.SUCCESS, dev.GetSramUniqueUncorrectedEccErrorCounts(&fill))

	counts := map[uint32]uint32{}
	for _, e := range entries {
		counts[e.Unit] = e.Count
	}
	require.Equal(t, map[uint32]uint32{
		sramUnitL2: 3, sramUnitMicrocontroller: 5, sramUnitOther: 1,
	}, counts)
}

func TestSramUniqueUncorrectedCounts_ShortBufferReportsTheNeededSize(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, sramSourcesConfig())

	entries := make([]nvml.EccSramUniqueUncorrectedErrorEntry_v1, 1)
	counts := nvml.EccSramUniqueUncorrectedErrorCounts{EntryCount: 1, Entries: &entries[0]}
	require.Equal(t, nvml.ERROR_INSUFFICIENT_SIZE, dev.GetSramUniqueUncorrectedEccErrorCounts(&counts))
	require.Equal(t, uint32(3), counts.EntryCount)
}

func TestSramUniqueUncorrectedCounts_Gating(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  *DeviceConfig
	}{
		{"before Blackwell", repairConfig("hopper", nil, nil)},
		{"ECC disabled", repairConfig("blackwell", nil, &ECCConfig{ModeCurrent: "disabled"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, tt.cfg)

			counts := nvml.EccSramUniqueUncorrectedErrorCounts{}
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.GetSramUniqueUncorrectedEccErrorCounts(&counts))
		})
	}
}

func TestSramUniqueUncorrectedCounts_HealthyDeviceHasNoEntries(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, repairConfig("blackwell", nil, nil))

	counts := nvml.EccSramUniqueUncorrectedErrorCounts{EntryCount: 9}
	require.Equal(t, nvml.SUCCESS, dev.GetSramUniqueUncorrectedEccErrorCounts(&counts))
	require.Zero(t, counts.EntryCount)
}

func inforomConfig() *DeviceConfig {
	return &DeviceConfig{InfoROM: &InfoROMConfig{ImageVersion: "G001.0000.02.04", ECCObject: "6.16"}}
}

func TestInforom_ValidatesAndChecksumIsStable(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, inforomConfig())
	same := newTestDeviceWithConfig(t, inforomConfig())
	other := newTestDeviceWithConfig(t, &DeviceConfig{InfoROM: &InfoROMConfig{ImageVersion: "G001.0000.03.01"}})

	require.Equal(t, nvml.SUCCESS, dev.ValidateInforom())

	sum, ret := dev.GetInforomConfigurationChecksum()
	require.Equal(t, nvml.SUCCESS, ret)
	sameSum, _ := same.GetInforomConfigurationChecksum()
	otherSum, _ := other.GetInforomConfigurationChecksum()
	require.Equal(t, sum, sameSum, "the checksum is a pure function of the InfoROM content")
	require.NotEqual(t, sum, otherSum, "a different InfoROM must not share a checksum")
	require.NotZero(t, sum)
}

func TestInforom_UnsupportedWithoutAnInforomBlock(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.ValidateInforom())
	_, ret := dev.GetInforomConfigurationChecksum()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func dramEncryptionConfig(arch string) *DeviceConfig {
	return repairConfig(arch, nil, &ECCConfig{
		ModeCurrent:    "enabled",
		DRAMEncryption: &ECCDRAMEncryptionConfig{ModeCurrent: "disabled", ModePending: "disabled"},
	})
}

func TestDramEncryption_UnsupportedBeforeBlackwell(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, dramEncryptionConfig("hopper"))

	_, _, ret := dev.GetDramEncryptionMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED,
		dev.SetDramEncryptionMode(&nvml.DramEncryptionInfo{EncryptionState: uint32(nvml.FEATURE_ENABLED)}))
}

func TestDramEncryption_DefaultsToDisabled(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, repairConfig("blackwell", nil, nil))

	current, pending, ret := dev.GetDramEncryptionMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(nvml.FEATURE_DISABLED), current.EncryptionState)
	require.Equal(t, uint32(nvml.FEATURE_DISABLED), pending.EncryptionState)
}

// TestSetDramEncryptionMode_TakesEffectAfterReboot follows the header: the
// pending mode is the target for the next reboot, so a write moves pending and
// leaves current alone.
func TestSetDramEncryptionMode_TakesEffectAfterReboot(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, dramEncryptionConfig("blackwell"))

	require.Equal(t, nvml.SUCCESS,
		dev.SetDramEncryptionMode(&nvml.DramEncryptionInfo{EncryptionState: uint32(nvml.FEATURE_ENABLED)}))

	current, pending, ret := dev.GetDramEncryptionMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(nvml.FEATURE_DISABLED), current.EncryptionState)
	require.Equal(t, uint32(nvml.FEATURE_ENABLED), pending.EncryptionState)
}

func TestSetDramEncryptionMode_RejectsAnInvalidState(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, dramEncryptionConfig("blackwell"))

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetDramEncryptionMode(nil))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
		dev.SetDramEncryptionMode(&nvml.DramEncryptionInfo{EncryptionState: 7}))
}

// TestSetEccMode_TakesEffectAfterReset follows the header: the new mode is
// pending until the GPU resets, so the current mode must not move.
func TestSetEccMode_TakesEffectAfterReset(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, repairConfig("hopper", nil, eccEnabled()))

	require.Equal(t, nvml.SUCCESS, dev.SetEccMode(nvml.FEATURE_DISABLED))

	current, pending, ret := dev.GetEccMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.FEATURE_ENABLED, current)
	require.Equal(t, nvml.FEATURE_DISABLED, pending)
}

func TestSetEccMode_RejectsAnInvalidState(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, repairConfig("hopper", nil, nil))

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetEccMode(nvml.EnableState(7)))
}

func TestSetEccMode_WithoutAWriterIsRefused(t *testing.T) {
	dev := newTestDeviceWithConfig(t, repairConfig("hopper", nil, nil))

	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.SetEccMode(nvml.FEATURE_DISABLED))
}

func eccWithCounts() *ECCConfig {
	dram := func(n uint64) *ECCErrorCountsConfig {
		return &ECCErrorCountsConfig{
			SingleBit: &ECCMemoryErrorsConfig{DeviceMemory: n, Total: n},
			DoubleBit: &ECCMemoryErrorsConfig{DeviceMemory: n, Total: n},
		}
	}
	return &ECCConfig{
		ModeCurrent: "enabled",
		Errors:      &ECCErrorsConfig{Volatile: dram(2), Aggregate: dram(9)},
		SRAM: &ECCSramConfig{
			Volatile:             &ECCSramCountsConfig{Correctable: 1, UncorrectableParity: 1},
			Aggregate:            &ECCSramCountsConfig{Correctable: 4, UncorrectableParity: 2, UncorrectableSECDED: 3},
			UncorrectableSources: &ECCSramSourcesConfig{L2: 5},
			ThresholdExceeded:    true,
		},
	}
}

func TestClearEccErrorCounts_VolatileLeavesAggregateAlone(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, repairConfig("hopper", nil, eccWithCounts()))

	require.Equal(t, nvml.SUCCESS, dev.ClearEccErrorCounts(nvml.VOLATILE_ECC))

	vol, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC)
	agg, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC)
	require.Zero(t, vol)
	require.Equal(t, uint64(9), agg)

	sram, ret := dev.GetSramEccErrorStatus()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, sram.VolatileCor)
	require.Zero(t, sram.VolatileUncParity)
	require.Equal(t, uint64(4), sram.AggregateCor)
	require.Equal(t, uint32(1), sram.BThresholdExceeded)
}

// TestClearEccErrorCounts_AggregateClearsTheSramFaultState covers the remediation
// path: once the persistent counters are cleared the GPU stops looking
// unrepairable, because the threshold they tripped is gone with them.
func TestClearEccErrorCounts_AggregateClearsTheSramFaultState(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, repairConfig("blackwell", nil, eccWithCounts()))

	flag, _ := dev.GetUnrepairableMemoryFlag()
	require.Equal(t, uint32(1), flag.BUnrepairableMemory)

	require.Equal(t, nvml.SUCCESS, dev.ClearEccErrorCounts(nvml.AGGREGATE_ECC))

	agg, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.AGGREGATE_ECC)
	require.Zero(t, agg)
	sram, _ := dev.GetSramEccErrorStatus()
	require.Zero(t, sram.AggregateUncParity)
	require.Zero(t, sram.AggregateUncBucketL2)
	require.Zero(t, sram.BThresholdExceeded)

	flag, _ = dev.GetUnrepairableMemoryFlag()
	require.Zero(t, flag.BUnrepairableMemory)

	counts := nvml.EccSramUniqueUncorrectedErrorCounts{}
	require.Equal(t, nvml.SUCCESS, dev.GetSramUniqueUncorrectedEccErrorCounts(&counts))
	require.Zero(t, counts.EntryCount)

	vol, _ := dev.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC)
	require.Equal(t, uint64(2), vol, "the volatile counters belong to the other scope")
}

func TestClearEccErrorCounts_RejectsAnUnknownCounterType(t *testing.T) {
	persistSetterWrites(t)
	dev := newTestDeviceWithConfig(t, repairConfig("hopper", nil, eccWithCounts()))

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.ClearEccErrorCounts(nvml.EccCounterType(9)))
}
