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
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
)

func makePlatformDevice(t *testing.T, cfg *DeviceConfig) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*mockserver.Device)
	return NewConfigurableDevice(0, bd, cfg,
		"GPU-00000000-0000-0000-0000-000000000000", "0000:01:00.0", 0, nil)
}

func TestGetMockPlatformInfo_ReportsConfiguredIdentity(t *testing.T) {
	dev := makePlatformDevice(t, &DeviceConfig{Platform: &PlatformConfig{
		ChassisSerialNumber: "1822725187334",
		SlotNumber:          26,
		TrayIndex:           16,
		HostID:              1,
		PeerType:            "switch_connected",
		ModuleID:            3,
	}})

	info, ret := dev.GetMockPlatformInfo()
	require.Equal(t, nvml.SUCCESS, ret, "return code")
	require.Equal(t, "1822725187334", info.chassisSerial(), "chassis serial number")
	require.Equal(t, uint8(26), info.SlotNumber, "slot number")
	require.Equal(t, uint8(16), info.TrayIndex, "tray index")
	require.Equal(t, uint8(1), info.HostID, "host id")
	require.Equal(t, PeerTypeSwitchConnected, info.PeerType, "peer type")
	require.Equal(t, uint8(3), info.ModuleID, "module id")
}

// The chassis serial travels as a fixed 16-byte buffer whose trailing bytes
// must be zero: nvidia-smi renders it as a NUL-terminated string, and NVML
// documents Blackwell as filling only the first 13 bytes.
func TestGetMockPlatformInfo_ChassisSerialIsZeroPadded(t *testing.T) {
	dev := makePlatformDevice(t, &DeviceConfig{Platform: &PlatformConfig{
		ChassisSerialNumber: "1822725187334",
	}})

	info, ret := dev.GetMockPlatformInfo()
	require.Equal(t, nvml.SUCCESS, ret, "return code")
	require.Equal(t, byte(0), info.ChassisSerialNumber[13], "byte after the 13-byte serial")
	require.Equal(t, [16]byte{}, PlatformInfo{}.ChassisSerialNumber, "zero value")
}

// A serial longer than the buffer is truncated with room kept for the
// terminator, so a misconfigured profile cannot hand nvidia-smi an
// unterminated string.
func TestGetMockPlatformInfo_ChassisSerialTruncatedToBuffer(t *testing.T) {
	dev := makePlatformDevice(t, &DeviceConfig{Platform: &PlatformConfig{
		ChassisSerialNumber: "12345678901234567890",
	}})

	info, ret := dev.GetMockPlatformInfo()
	require.Equal(t, nvml.SUCCESS, ret, "return code")
	require.Equal(t, "123456789012345", info.chassisSerial(), "truncated serial")
	require.Equal(t, byte(0), info.ChassisSerialNumber[15], "terminator")
}

// The negative direction is what keeps the fix from degenerating into
// hardcoded constants on every board: a profile with no platform block is a
// GPU whose platform cannot report a physical location, which nvidia-smi
// renders as N/A across the whole block.
func TestGetMockPlatformInfo_NotSupportedWithoutPlatformBlock(t *testing.T) {
	for name, cfg := range map[string]*DeviceConfig{
		"no platform block": {},
		"empty config":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			dev := makePlatformDevice(t, cfg)
			info, ret := dev.GetMockPlatformInfo()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "return code")
			require.Equal(t, PlatformInfo{}, info, "platform info")
		})
	}
}

func TestParsePeerType(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want uint8
	}{
		"switch connected":       {"switch_connected", PeerTypeSwitchConnected},
		"direct connected":       {"direct_connected", PeerTypeDirectConnected},
		"mixed case and spacing": {" Switch_Connected ", PeerTypeSwitchConnected},
		"switch shorthand":       {"switch", PeerTypeSwitchConnected},
		"direct shorthand":       {"direct", PeerTypeDirectConnected},
		"empty defaults direct":  {"", PeerTypeDirectConnected},
		"unknown means direct":   {"nonsense", PeerTypeDirectConnected},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, parsePeerType(tc.in))
		})
	}
}

// GetMockModuleID answers from the same configured identity as the
// PlatformInfo block, so a consumer reading either API sees one GPU.
func TestGetMockModuleID(t *testing.T) {
	dev := makePlatformDevice(t, &DeviceConfig{Platform: &PlatformConfig{ModuleID: 5}})
	id, ret := dev.GetMockModuleID()
	require.Equal(t, nvml.SUCCESS, ret, "return code")
	require.Equal(t, uint32(5), id, "module id")

	bare := makePlatformDevice(t, &DeviceConfig{})
	id, ret = bare.GetMockModuleID()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "return code without a platform block")
	require.Zero(t, id, "module id without a platform block")
}
