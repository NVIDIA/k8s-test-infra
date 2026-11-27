// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func bankRemapperConfig(arch string, banks *BankRemapperConfig) *DeviceConfig {
	return &DeviceConfig{Architecture: arch, BankRemapper: banks}
}

func degradedBanks() *BankRemapperConfig {
	return &BankRemapperConfig{
		ActiveRemappings:   3,
		InactiveRemappings: 1,
		Pending:            true,
		Histogram:          &BankRemapperHistogramConfig{MaxSpareGroups: 250, NoSpareGroups: 2},
	}
}

// The bank remapper is Rubin hardware: an older board answers NOT_SUPPORTED
// even when the profile describes one.
func TestBankRemapperStatus_UnsupportedBeforeRubin(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"ampere", "hopper", "blackwell"} {
		dev := newTestDeviceWithConfig(t, bankRemapperConfig(arch, degradedBanks()))

		_, ret := dev.GetBankRemapperStatus_v1()
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, arch)
	}
}

func TestBankRemapperStatus_ReportsTheProfile(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, bankRemapperConfig("rubin", degradedBanks()))

	status, ret := dev.GetBankRemapperStatus_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.EccBankRemapperStatus_v1{
		ActiveRemappings:   3,
		InactiveRemappings: 1,
		BPending:           1,
		Histogram:          nvml.EccBankRemapperHistogram_v1{MaxSpareGroupCount: 250, NoSpareGroupCount: 2},
	}, status)
}

// A profile that leaves the block out has a remapper with nothing remapped and
// nothing pending, unless it marks absent features unsupported.
func TestBankRemapperStatus_AbsentBlock(t *testing.T) {
	t.Parallel()
	legacy := newTestDeviceWithConfig(t, bankRemapperConfig("rubin", nil))
	status, ret := legacy.GetBankRemapperStatus_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, status)

	strict := shippedProfileDevice(t, "vrnvl72", "", func(yc *YAMLConfig) { yc.DeviceDefaults.BankRemapper = nil })
	_, ret = strict.GetBankRemapperStatus_v1()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// The shipped Rubin profile reproduces the captured VR NVL72 tray.
func TestBankRemapperStatus_VRNVL72MatchesTheCapture(t *testing.T) {
	t.Parallel()
	dev := shippedProfileDevice(t, "vrnvl72", "")

	status, ret := dev.GetBankRemapperStatus_v1()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.EccBankRemapperStatus_v1{
		Histogram: nvml.EccBankRemapperHistogram_v1{MaxSpareGroupCount: 252},
	}, status)
}

// The field ids read the same state as the getter, as unsigned long longs, and
// share its answer when it has none.
func TestBankRemapperFieldValues(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, bankRemapperConfig("rubin", degradedBanks()))
	for id, want := range map[uint32]uint64{
		nvml.FI_DEV_ACTIVE_BANK_REMAPPINGS:       3,
		nvml.FI_DEV_INACTIVE_BANK_REMAPPINGS:     1,
		nvml.FI_DEV_BANK_REMAPPER_HISTOGRAM_MAX:  250,
		nvml.FI_DEV_BANK_REMAPPER_HISTOGRAM_NONE: 2,
		nvml.FI_DEV_PENDING_BANK_REMAPPING:       1,
	} {
		vt, val, ret := dev.GetFieldValue(id, 0)
		require.Equal(t, nvml.SUCCESS, ret, "field %d", id)
		require.Equal(t, FieldValueUint64, vt, "field %d", id)
		require.Equal(t, want, val, "field %d", id)
	}

	hopper := newTestDeviceWithConfig(t, bankRemapperConfig("hopper", degradedBanks()))
	_, _, ret := hopper.GetFieldValue(nvml.FI_DEV_ACTIVE_BANK_REMAPPINGS, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestBankRemapperStatus_DeviceOverrideReplacesTheBlock(t *testing.T) {
	t.Parallel()
	base := DeviceConfig{BankRemapper: &BankRemapperConfig{ActiveRemappings: 1}}
	mergeDeviceOverride(&base, &DeviceOverride{DeviceConfig: DeviceConfig{BankRemapper: degradedBanks()}})
	require.Equal(t, degradedBanks(), base.BankRemapper)
}
