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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetDeviceConfigPCIOverrideDoesNotLeakIntoDefaults covers issue #589:
// GetDeviceConfig shallow-copies DeviceDefaults, so the copy shares the
// *PCIConfig with the defaults. mergeDeviceOverride is the only merge branch
// that writes THROUGH that pointer, so a per-device PCI override used to be
// stamped onto the shared defaults and inherited by every device merged after
// it.
func TestGetDeviceConfigPCIOverrideDoesNotLeakIntoDefaults(t *testing.T) {
	t.Parallel()

	const (
		defaultDeviceID    uint32 = 0x20B010DE // A100-SXM4-80GB
		overrideDeviceID   uint32 = 0x233010DE // H100-SXM5-80GB
		defaultSubsystemID uint32 = 0x144E10DE
		defaultBusID              = "00000000:07:00.0"
	)

	cfg := &Config{
		NumDevices: 3,
		YAMLConfig: &YAMLConfig{
			DeviceDefaults: DeviceConfig{
				PCI: &PCIConfig{
					DeviceID:    defaultDeviceID,
					SubsystemID: defaultSubsystemID,
					BusID:       defaultBusID,
				},
			},
			Devices: []DeviceOverride{
				{
					Index:        0,
					DeviceConfig: DeviceConfig{PCI: &PCIConfig{DeviceID: overrideDeviceID}},
				},
			},
		},
	}

	// Device 0 carries the override, and merging it is what corrupts the
	// shared defaults, so it must be resolved before the other devices.
	t.Run("overridden device gets the override plus untouched defaults", func(t *testing.T) {
		dev0 := cfg.GetDeviceConfig(0)
		require.NotNil(t, dev0)
		require.NotNil(t, dev0.PCI)

		assert.Equal(t, overrideDeviceID, dev0.PCI.DeviceID, "device 0 must report its own device ID")
		// Fields the override leaves zero still come from the defaults.
		assert.Equal(t, defaultSubsystemID, dev0.PCI.SubsystemID, "device 0 subsystem ID must fall back to the default")
		assert.Equal(t, defaultBusID, dev0.PCI.BusID, "device 0 bus ID must fall back to the default")
	})

	for _, index := range []int{1, 2} {
		t.Run(fmt.Sprintf("device %d keeps the default device ID", index), func(t *testing.T) {
			dev := cfg.GetDeviceConfig(index)
			require.NotNil(t, dev)
			require.NotNil(t, dev.PCI)

			assert.Equal(t, defaultDeviceID, dev.PCI.DeviceID,
				"device 0's PCI override leaked into device %d through the shared defaults", index)
		})
	}

	t.Run("shared defaults are not mutated", func(t *testing.T) {
		assert.Equal(t, defaultDeviceID, cfg.YAMLConfig.DeviceDefaults.PCI.DeviceID,
			"GetDeviceConfig wrote device 0's override onto YAMLConfig.DeviceDefaults")
	})

	t.Run("resolving a default-only device is repeatable", func(t *testing.T) {
		// If the defaults were mutated, every later resolve keeps returning the
		// corrupted value rather than re-deriving from a clean base.
		again := cfg.GetDeviceConfig(1)
		require.NotNil(t, again)
		require.NotNil(t, again.PCI)

		assert.Equal(t, defaultDeviceID, again.PCI.DeviceID, "second resolve of device 1 must still see the default")
	})
}
