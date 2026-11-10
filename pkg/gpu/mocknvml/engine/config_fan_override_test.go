// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGetDeviceConfigMergesPerDeviceFanBlock covers a silent drop in
// mergeDeviceOverride: the fan block was parsed off a per-device override and
// then discarded, so every device inherited the defaults' fan count no matter
// what it declared.
//
// It matters for a profile that mixes form factors — a fanned PCIe board in the
// same node as fanless SXM modules — and it is invisible from the outside,
// because the answer a dropped override produces is the same NOT_SUPPORTED a
// genuinely fanless board gives.
func TestGetDeviceConfigMergesPerDeviceFanBlock(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		NumDevices: 2,
		YAMLConfig: &YAMLConfig{
			DeviceDefaults: DeviceConfig{
				Fan: &FanConfig{Count: 0, SpeedPercent: "N/A"},
			},
			Devices: []DeviceOverride{
				{
					Index: 1,
					DeviceConfig: DeviceConfig{
						Fan: &FanConfig{Count: 2, SpeedPercent: "40", MaxSpeedRPM: 5000},
					},
				},
			},
		},
	}

	fanned := cfg.GetDeviceConfig(1).Fan
	require.NotNil(t, fanned)
	require.Equal(t, 2, fanned.Count, "device 1 declares two fans")
	require.Equal(t, "40", fanned.SpeedPercent)
	require.Equal(t, 5000, fanned.MaxSpeedRPM)

	// The override must not leak onto the shared defaults, which is the
	// failure mode issue #589 documented for the PCI branch.
	require.Equal(t, 0, cfg.GetDeviceConfig(0).Fan.Count,
		"device 0 inherited device 1's fan override")
}
