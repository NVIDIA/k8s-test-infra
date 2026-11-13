// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGetDeviceConfigMergesPerDeviceAccountingBlock covers accounting as
// per-board state: mergeDeviceOverride copies only the fields it names, so a
// device's accounting block must be one of them to take effect, and must stay
// on that device rather than reaching its siblings.
func TestGetDeviceConfigMergesPerDeviceAccountingBlock(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		NumDevices: 2,
		YAMLConfig: &YAMLConfig{
			DeviceDefaults: DeviceConfig{
				Accounting: &AccountingConfig{Mode: "disabled", BufferSize: 4000},
			},
			Devices: []DeviceOverride{
				{
					Index: 1,
					DeviceConfig: DeviceConfig{
						Accounting: &AccountingConfig{
							Mode:       "enabled",
							Terminated: []AccountedProcessConfig{{PID: 300}},
						},
					},
				},
			},
		},
	}

	accounted := cfg.GetDeviceConfig(1).Accounting
	require.NotNil(t, accounted)
	require.Equal(t, "enabled", accounted.Mode)
	require.Len(t, accounted.Terminated, 1)

	require.Equal(t, "disabled", cfg.GetDeviceConfig(0).Accounting.Mode,
		"device 0 inherited device 1's accounting override")
}
