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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// gracePlatformProfiles are the rack-scale profiles expected to describe a
// physical location. Every other shipped profile models a board whose platform
// cannot report one, so it must leave the whole block absent — a new profile
// has to choose a side here rather than defaulting into the untested one.
var gracePlatformProfiles = map[string]bool{
	"gb200.yaml": true,
	"gb300.yaml": true,
	"a100.yaml":  false,
	"b200.yaml":  false,
	"h100.yaml":  false,
	"l40s.yaml":  false,
	"t4.yaml":    false,
}

// TestProfilePlatformIdentity pins the layout the NVL72 profiles ship: the
// node-level fields shared across the node's GPUs, and a module id that is
// distinct per GPU so a physical location can be derived per device. Loading
// through Config.GetDeviceConfig is what makes the distinctness meaningful —
// that is the merge path a device's getters read, and a shared PlatformConfig
// pointer would collapse all eight module ids onto whichever merged last.
func TestProfilePlatformIdentity(t *testing.T) {
	for file, grace := range gracePlatformProfiles {
		t.Run(file, func(t *testing.T) {
			yamlCfg, err := LoadYAMLConfig(filepath.Join(testdataDir(), file))
			require.NoError(t, err, "loading profile")
			cfg := &Config{YAMLConfig: yamlCfg, NumDevices: len(yamlCfg.Devices)}

			if !grace {
				require.Nil(t, yamlCfg.DeviceDefaults.Platform, "device_defaults.platform")
				for i := range yamlCfg.Devices {
					require.Nil(t, cfg.GetDeviceConfig(i).Platform, "device %d platform", i)
				}
				return
			}

			defaults := yamlCfg.DeviceDefaults.Platform
			require.NotNil(t, defaults, "device_defaults.platform")
			require.NotEmpty(t, defaults.ChassisSerialNumber, "chassis_serial_number")
			require.NotZero(t, defaults.SlotNumber, "slot_number")
			require.NotZero(t, defaults.TrayIndex, "tray_index")
			require.NotZero(t, defaults.HostID, "host_id")
			require.Equal(t, PeerTypeSwitchConnected, parsePeerType(defaults.PeerType),
				"peer_type: an NVL72 GPU reaches its peers through a switch tray")
			// A compute tray sits above the rack's switch trays, which the
			// slot numbering counts and the tray index does not.
			require.Greater(t, defaults.SlotNumber, defaults.TrayIndex,
				"slot_number must lead tray_index: slots count switch trays too")

			seen := map[uint8]int{}
			for i := range yamlCfg.Devices {
				platform := cfg.GetDeviceConfig(i).Platform
				require.NotNil(t, platform, "device %d platform", i)
				require.NotZero(t, platform.ModuleID, "device %d module_id", i)
				if prev, dup := seen[platform.ModuleID]; dup {
					t.Fatalf("device %d shares module_id %d with device %d: a GPU's "+
						"physical location must identify it", i, platform.ModuleID, prev)
				}
				seen[platform.ModuleID] = i

				// The rest of the identity describes the node, so every GPU
				// must agree on it.
				require.Equal(t, defaults.ChassisSerialNumber, platform.ChassisSerialNumber, "device %d chassis", i)
				require.Equal(t, defaults.SlotNumber, platform.SlotNumber, "device %d slot", i)
				require.Equal(t, defaults.TrayIndex, platform.TrayIndex, "device %d tray", i)
				require.Equal(t, defaults.HostID, platform.HostID, "device %d host", i)
				require.Equal(t, defaults.PeerType, platform.PeerType, "device %d peer type", i)
			}
			require.Len(t, seen, len(yamlCfg.Devices), "distinct module ids")
		})
	}
}

// A per-device block must not leak into the defaults every other device merges
// from, which is the mechanism that made all eight GPUs report one module id.
func TestMergePlatformOverride_LeavesDefaultsIntact(t *testing.T) {
	defaults := DeviceConfig{Platform: &PlatformConfig{
		ChassisSerialNumber: "1822725100200",
		SlotNumber:          21,
		ModuleID:            1,
	}}
	cfg := &Config{YAMLConfig: &YAMLConfig{
		DeviceDefaults: defaults,
		Devices: []DeviceOverride{
			{Index: 0, DeviceConfig: DeviceConfig{Platform: &PlatformConfig{ModuleID: 7}}},
			{Index: 1},
		},
	}}

	require.Equal(t, uint8(7), cfg.GetDeviceConfig(0).Platform.ModuleID, "overridden device")
	require.Equal(t, uint8(1), cfg.GetDeviceConfig(1).Platform.ModuleID, "device without an override")
	require.Equal(t, uint8(1), defaults.Platform.ModuleID, "device_defaults after merging")
	// The fields the override stayed silent about still come from the defaults.
	require.Equal(t, "1822725100200", cfg.GetDeviceConfig(0).Platform.ChassisSerialNumber, "chassis serial")
	require.Equal(t, uint8(21), cfg.GetDeviceConfig(0).Platform.SlotNumber, "slot number")
}
