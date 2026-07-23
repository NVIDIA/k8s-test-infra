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
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// injectionDevice builds a single-GPU engine whose device 0 has `links` active
// switch links and an optional NVLinkError injection block, then returns the
// live *ConfigurableDevice with its fabric clock pinned to a fixed epoch so
// error accrual is deterministic. now is the sample time relative to epoch.
func injectionDevice(t *testing.T, links int, inj *NVLinkErrorInjectionConfig, elapsed time.Duration) *ConfigurableDevice {
	t.Helper()
	dev := devWithBDF(0, "0000:0A:00.0")
	dev.DeviceConfig.NVLinkError = inj
	yc := &YAMLConfig{
		System:  SystemConfig{DriverVersion: "560.0", NumDevices: 1},
		Devices: []DeviceOverride{dev},
		NVLink: &NVLinkConfig{
			Version:              5,
			BandwidthPerLinkMbps: 100000,
			Switches:             []NVSwitchConfig{{BDF: "0000:0F:00.0"}},
			Defaults:             &NVLinkDefaults{State: "active"},
			DeviceLinks: []DeviceLinksConfig{
				{Index: 0, Links: switchLinks(links, "0000:0F:00.0")},
			},
		},
	}
	e := NewEngine(&Config{NumDevices: 1, YAMLConfig: yc})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })

	handle, _ := e.DeviceGetHandleByIndex(0)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok, "expected ConfigurableDevice")

	epoch := time.Unix(1_000_000, 0)
	cd.fabric.epoch = epoch
	cd.fabric.now = func() time.Time { return epoch.Add(elapsed) }
	return cd
}

// TestNVLinkErrorInjection_AccruesOverTime asserts an injected rate makes the
// per-link error counter climb monotonically — the rising rate DCGM's NVLink
// health watch keys on — and that the field-value path agrees with the direct
// API.
func TestNVLinkErrorInjection_AccruesOverTime(t *testing.T) {
	cd := injectionDevice(t, 4, &NVLinkErrorInjectionConfig{Rate: 100}, 10*time.Second)

	got, ret := cd.GetNvLinkErrorCounter(0, nvml.NVLINK_ERROR_DL_CRC_FLIT)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(1000), got, "100 err/s * 10s")

	// The DL error field values (161-163) must match the direct API so
	// `dcgmi dmon` and nvmlDeviceGetNvLinkErrorCounter tell the same story.
	for _, fieldID := range []uint32{fiNvlinkErrorDlReplay, fiNvlinkErrorDlRecovery, fiNvlinkErrorDlCrc} {
		ft, v, fret := cd.GetNvLinkFieldValue(fieldID, 0)
		require.Equal(t, nvml.SUCCESS, fret, "field %d", fieldID)
		require.Equal(t, FieldValueUint64, ft, "field %d", fieldID)
		require.Equal(t, uint64(1000), v, "field %d should match direct API", fieldID)
	}
}

// TestNVLinkErrorInjection_HealthyByDefault asserts a device with no injection
// (and no configured error rate) reports zero errors regardless of elapsed time.
func TestNVLinkErrorInjection_HealthyByDefault(t *testing.T) {
	cd := injectionDevice(t, 4, nil, 24*time.Hour)
	got, ret := cd.GetNvLinkErrorCounter(0, nvml.NVLINK_ERROR_DL_CRC_FLIT)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, got, "no injection should read 0 errors")
}

// TestNVLinkErrorInjection_ZeroRateHeals asserts an explicit rate of 0 is the
// healthy value (the `nvlink-error 0` heal path), not an injection.
func TestNVLinkErrorInjection_ZeroRateHeals(t *testing.T) {
	cd := injectionDevice(t, 4, &NVLinkErrorInjectionConfig{Rate: 0}, time.Hour)
	got, ret := cd.GetNvLinkErrorCounter(0, nvml.NVLINK_ERROR_DL_CRC_FLIT)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, got, "rate 0 heals")
}

// TestNVLinkErrorInjection_LinkFilter asserts the Links filter restricts
// injection to the listed link ids; other active links stay healthy.
func TestNVLinkErrorInjection_LinkFilter(t *testing.T) {
	cd := injectionDevice(t, 4, &NVLinkErrorInjectionConfig{Rate: 100, Links: []int{2}}, 10*time.Second)

	hot, _ := cd.GetNvLinkErrorCounter(2, nvml.NVLINK_ERROR_DL_CRC_FLIT)
	require.Equal(t, uint64(1000), hot, "filtered-in link should accrue")

	for _, link := range []int{0, 1, 3} {
		cool, _ := cd.GetNvLinkErrorCounter(link, nvml.NVLINK_ERROR_DL_CRC_FLIT)
		require.Zerof(t, cool, "link %d not in filter should stay healthy", link)
	}
}

// TestNVLinkErrorInjection_InactiveLinkStaysAbsent asserts injection never
// lands on a link the device does not actually have active — a link that is in
// NVML's valid index range but unpopulated must not be conjured into an errored
// one (the device here only has 2 active links; link 5 is inactive).
func TestNVLinkErrorInjection_InactiveLinkStaysAbsent(t *testing.T) {
	cd := injectionDevice(t, 2, &NVLinkErrorInjectionConfig{Rate: 100}, 10*time.Second)
	got, ret := cd.GetNvLinkErrorCounter(5, nvml.NVLINK_ERROR_DL_CRC_FLIT)
	require.Equal(t, nvml.SUCCESS, ret, "link 5 is within NVML range")
	require.Zero(t, got, "inactive link must stay healthy")
}
