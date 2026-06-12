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
)

// newSwitchFabricDevice builds an 8-GPU GB200-like device whose 18 links are
// produced by switch-link auto-expansion (the same path the shipped gb200
// profile uses), and returns device 0.
func newSwitchFabricDevice(t *testing.T) *ConfigurableDevice {
	t.Helper()
	yaml := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.35.03", NumDevices: 8},
		NVLink: &NVLinkConfig{
			Version:              5,
			LinksPerGPU:          18,
			BandwidthPerLinkGBPS: 53,
			Switches: []NVSwitchConfig{
				{BDF: "0000:01:00.0"},
				{BDF: "0000:02:00.0"},
				{BDF: "0000:03:00.0"},
				{BDF: "0000:04:00.0"},
			},
		},
	}
	cfg := &Config{NumDevices: 8, DriverVersion: "560.35.03", YAMLConfig: yaml}
	e := NewEngine(cfg)
	if ret := e.Init(); ret != nvml.SUCCESS {
		t.Fatalf("engine init: %v", ret)
	}
	t.Cleanup(func() { _ = e.Shutdown() })

	handle, _ := e.DeviceGetHandleByIndex(0)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	if !ok {
		t.Fatal("expected ConfigurableDevice")
	}
	return cd
}

// TestGetNvLinkFieldValue_SwitchFabric is the regression guard for the empty
// `nvidia-smi nvlink -s/-c/-e` output: nvidia-smi reads LINK_COUNT first and
// prints nothing if it is unset/NOT_SUPPORTED. Every link must therefore
// report a count and per-link facts off the auto-expanded fabric.
func TestGetNvLinkFieldValue_SwitchFabric(t *testing.T) {
	cd := newSwitchFabricDevice(t)

	t.Run("link_count", func(t *testing.T) {
		vt, val, ret := cd.GetNvLinkFieldValue(fiNvlinkLinkCount, 0)
		if ret != nvml.SUCCESS || vt != NVLinkFieldUint || val != 18 {
			t.Fatalf("LINK_COUNT = (type=%d,val=%d,ret=%v), want (Uint,18,SUCCESS)", vt, val, ret)
		}
	})

	t.Run("state_active", func(t *testing.T) {
		_, val, ret := cd.GetNvLinkFieldValue(fiNvlinkGetState, 0)
		if ret != nvml.SUCCESS || val != 1 {
			t.Errorf("GET_STATE(link0) = (val=%d,ret=%v), want (1,SUCCESS)", val, ret)
		}
	})

	t.Run("state_out_of_range_unsupported", func(t *testing.T) {
		vt, _, ret := cd.GetNvLinkFieldValue(fiNvlinkGetState, 99)
		if ret != nvml.ERROR_NOT_SUPPORTED || vt != NVLinkFieldUnsupported {
			t.Errorf("GET_STATE(link99) = (type=%d,ret=%v), want (Unsupported,NOT_SUPPORTED)", vt, ret)
		}
	})

	t.Run("version", func(t *testing.T) {
		_, val, ret := cd.GetNvLinkFieldValue(fiNvlinkGetVersion, 0)
		if ret != nvml.SUCCESS || val != 5 {
			t.Errorf("GET_VERSION(link0) = (val=%d,ret=%v), want (5,SUCCESS)", val, ret)
		}
	})

	t.Run("speed_mbps", func(t *testing.T) {
		// 53 GB/s/link -> 53 * 1e9 bytes/s / 1e6 = 53000 MB/s.
		for _, fid := range []uint32{fiNvlinkGetSpeed, fiNvlinkSpeedMbpsCommon, fiNvlinkSpeedMbpsL0} {
			vt, val, ret := cd.GetNvLinkFieldValue(fid, 0)
			if ret != nvml.SUCCESS || vt != NVLinkFieldUint || val != 53000 {
				t.Errorf("speed field %d = (type=%d,val=%d,ret=%v), want (Uint,53000,SUCCESS)", fid, vt, val, ret)
			}
		}
	})

	t.Run("throughput_is_uint64", func(t *testing.T) {
		vt, _, ret := cd.GetNvLinkFieldValue(fiNvlinkThroughputDataRx, 0)
		if ret != nvml.SUCCESS || vt != NVLinkFieldUint64 {
			t.Errorf("THROUGHPUT_DATA_RX = (type=%d,ret=%v), want (Uint64,SUCCESS)", vt, ret)
		}
	})

	t.Run("unknown_field_unsupported", func(t *testing.T) {
		vt, _, ret := cd.GetNvLinkFieldValue(99999, 0)
		if ret != nvml.ERROR_NOT_SUPPORTED || vt != NVLinkFieldUnsupported {
			t.Errorf("unknown field = (type=%d,ret=%v), want (Unsupported,NOT_SUPPORTED)", vt, ret)
		}
	})

	// `nvidia-smi nvlink -e` reads the NVLink5 COUNT_* fields; each must be
	// present (SUCCESS) for an active link, else the line is omitted.
	t.Run("nvlink_e_counters_present", func(t *testing.T) {
		ullFields := []uint32{
			fiNvlinkCountXmitPackets, fiNvlinkCountXmitBytes,
			fiNvlinkCountRcvPackets, fiNvlinkCountRcvBytes,
			fiNvlinkCountMalformedPacketErrors, fiNvlinkCountBufferOverrunErrors,
			fiNvlinkCountRcvErrors, fiNvlinkCountRcvRemoteErrors,
			fiNvlinkCountRcvGeneralErrors, fiNvlinkCountLocalLinkIntegrityErrors,
			fiNvlinkCountXmitDiscards, fiNvlinkCountLinkRecoverySuccessfulEvents,
			fiNvlinkCountLinkRecoveryFailedEvents, fiNvlinkCountLinkRecoveryEvents,
			fiNvlinkCountEffectiveErrors, fiNvlinkCountSymbolErrors,
		}
		for _, fid := range ullFields {
			vt, _, ret := cd.GetNvLinkFieldValue(fid, 0)
			if ret != nvml.SUCCESS || vt != NVLinkFieldUint64 {
				t.Errorf("field %d = (type=%d,ret=%v), want (Uint64,SUCCESS)", fid, vt, ret)
			}
		}
		// FEC histogram: all 16 bins present.
		for fid := uint32(fiNvlinkCountFecHistory0); fid <= fiNvlinkCountFecHistory15; fid++ {
			vt, _, ret := cd.GetNvLinkFieldValue(fid, 0)
			if ret != nvml.SUCCESS || vt != NVLinkFieldUint64 {
				t.Errorf("FEC field %d = (type=%d,ret=%v), want (Uint64,SUCCESS)", fid, vt, ret)
			}
		}
		// BER fields are doubles.
		for _, fid := range []uint32{fiNvlinkCountEffectiveBer, fiNvlinkCountSymbolBer} {
			vt, _, ret := cd.GetNvLinkFieldValue(fid, 0)
			if ret != nvml.SUCCESS || vt != NVLinkFieldDouble {
				t.Errorf("BER field %d = (type=%d,ret=%v), want (Double,SUCCESS)", fid, vt, ret)
			}
		}
	})

	// The packet/byte/FEC magnitudes must track the deterministic base counter:
	// bytes == packets*86, FEC bin0 == packets*107, and errors stay zero.
	t.Run("nvlink_e_counter_ratios", func(t *testing.T) {
		// Freeze the clock so the accruing base counter is identical across the
		// three reads (production reads each field independently; here we assert
		// the cross-field derivation, which requires a fixed sample point).
		fixed := cd.fabric.epoch.Add(time.Hour)
		cd.fabric.now = func() time.Time { return fixed }

		_, pkts, _ := cd.GetNvLinkFieldValue(fiNvlinkCountXmitPackets, 0)
		_, bytes, _ := cd.GetNvLinkFieldValue(fiNvlinkCountXmitBytes, 0)
		_, fec0, _ := cd.GetNvLinkFieldValue(fiNvlinkCountFecHistory0, 0)
		if bytes != pkts*avgBytesPerNvlinkPacket {
			t.Errorf("bytes=%d, want packets*%d=%d", bytes, avgBytesPerNvlinkPacket, pkts*avgBytesPerNvlinkPacket)
		}
		if fec0 != pkts*107 {
			t.Errorf("fec0=%d, want packets*107=%d", fec0, pkts*107)
		}
		if _, errs, _ := cd.GetNvLinkFieldValue(fiNvlinkCountRcvErrors, 0); errs != 0 {
			t.Errorf("rcv errors=%d, want 0 (clean link)", errs)
		}
	})
}

// TestGetNvLinkFieldValue_RemoteAndLowPower covers the fields that back
// `nvidia-smi nvlink -R` (remote NVLink id) and `-gLowPwrInfo` (single-lane
// power state + threshold range). On a switch fabric every active GPU link
// reaches the far end at link 0 and reports the High Speed state.
func TestGetNvLinkFieldValue_RemoteAndLowPower(t *testing.T) {
	cd := newSwitchFabricDevice(t)

	// `topo -m` (580 binary) reads NVSWITCH_CONNECTED_LINK_COUNT per GPU to
	// detect the switch fabric; the 18 auto-expanded links all land on switches.
	t.Run("nvswitch_connected_link_count", func(t *testing.T) {
		vt, val, ret := cd.GetNvLinkFieldValue(fiNvswitchConnectedLinkCount, 0)
		if ret != nvml.SUCCESS || vt != NVLinkFieldUint || val != 18 {
			t.Fatalf("NVSWITCH_CONNECTED_LINK_COUNT = (type=%d,val=%d,ret=%v), want (Uint,18,SUCCESS)", vt, val, ret)
		}
	})

	t.Run("remote_nvlink_id_active", func(t *testing.T) {
		vt, val, ret := cd.GetNvLinkFieldValue(fiNvlinkRemoteNvlinkID, 0)
		if ret != nvml.SUCCESS || vt != NVLinkFieldUint || val != 0 {
			t.Fatalf("REMOTE_NVLINK_ID(link0) = (type=%d,val=%d,ret=%v), want (Uint,0,SUCCESS)", vt, val, ret)
		}
	})

	t.Run("remote_nvlink_id_out_of_range", func(t *testing.T) {
		vt, _, ret := cd.GetNvLinkFieldValue(fiNvlinkRemoteNvlinkID, 99)
		if ret != nvml.ERROR_NOT_SUPPORTED || vt != NVLinkFieldUnsupported {
			t.Errorf("REMOTE_NVLINK_ID(link99) = (type=%d,ret=%v), want (Unsupported,NOT_SUPPORTED)", vt, ret)
		}
	})

	t.Run("low_power_per_link", func(t *testing.T) {
		if _, val, ret := cd.GetNvLinkFieldValue(fiNvlinkGetPowerState, 0); ret != nvml.SUCCESS || val != nvlinkPowerStateHighSpeed {
			t.Errorf("GET_POWER_STATE(link0) = (val=%d,ret=%v), want (%d,SUCCESS)", val, ret, nvlinkPowerStateHighSpeed)
		}
		if _, val, ret := cd.GetNvLinkFieldValue(fiNvlinkGetPowerThreshold, 0); ret != nvml.SUCCESS || val != lowPowerThresholdDefault {
			t.Errorf("GET_POWER_THRESHOLD(link0) = (val=%d,ret=%v), want (%d,SUCCESS)", val, ret, lowPowerThresholdDefault)
		}
	})

	t.Run("low_power_header", func(t *testing.T) {
		cases := map[uint32]uint64{
			fiNvlinkPowerThresholdUnits:     lowPowerThresholdUnit50us,
			fiNvlinkPowerThresholdMin:       lowPowerThresholdMin,
			fiNvlinkPowerThresholdMax:       lowPowerThresholdMax,
			fiNvlinkPowerThresholdSupported: nvlinkPowerThresholdEnabled,
		}
		for fid, want := range cases {
			vt, val, ret := cd.GetNvLinkFieldValue(fid, 0)
			if ret != nvml.SUCCESS || vt != NVLinkFieldUint || val != want {
				t.Errorf("field %d = (type=%d,val=%d,ret=%v), want (Uint,%d,SUCCESS)", fid, vt, val, ret, want)
			}
		}
	})

	// `nvlink -p`/`-R`: switch-attached links report the all-0xFF "invalid"
	// remote PCI sentinel (a real GB200 shows FFFFFFFF:FF:FF.0 for every link).
	t.Run("switch_remote_pci_sentinel", func(t *testing.T) {
		pci, ret := cd.GetNvLinkRemotePciInfo(0)
		if ret != nvml.SUCCESS {
			t.Fatalf("GetNvLinkRemotePciInfo(0): %v", ret)
		}
		if pci.Domain != 0xFFFFFFFF || pci.Bus != 0xFF || pci.Device != 0xFF {
			t.Errorf("sentinel = domain=%#x bus=%#x device=%#x, want all-0xFF", pci.Domain, pci.Bus, pci.Device)
		}
		if got := busIDString(pci.BusId[:]); got != invalidRemotePciBusID {
			t.Errorf("sentinel BusId = %q, want %q", got, invalidRemotePciBusID)
		}
	})
}

// TestGetNvLinkFieldValue_NoNVLink verifies a profile without NVLink reports a
// zero link count (SUCCESS), so nvidia-smi correctly shows no NVLinks rather
// than erroring.
func TestGetNvLinkFieldValue_NoNVLink(t *testing.T) {
	cfg := &Config{NumDevices: 1, DriverVersion: "550.0", YAMLConfig: &YAMLConfig{
		System: SystemConfig{DriverVersion: "550.0", NumDevices: 1},
	}}
	e := NewEngine(cfg)
	if ret := e.Init(); ret != nvml.SUCCESS {
		t.Fatalf("engine init: %v", ret)
	}
	t.Cleanup(func() { _ = e.Shutdown() })

	handle, _ := e.DeviceGetHandleByIndex(0)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	if !ok {
		t.Fatal("expected ConfigurableDevice")
	}

	vt, val, ret := cd.GetNvLinkFieldValue(fiNvlinkLinkCount, 0)
	if ret != nvml.SUCCESS || vt != NVLinkFieldUint || val != 0 {
		t.Errorf("LINK_COUNT (no nvlink) = (type=%d,val=%d,ret=%v), want (Uint,0,SUCCESS)", vt, val, ret)
	}

	// Low-power info must report NOT_SUPPORTED when the GPU has no NVLinks, so
	// `nvlink -gLowPwrInfo` shows nothing rather than a bogus threshold range.
	for _, fid := range []uint32{
		fiNvlinkPowerThresholdUnits, fiNvlinkPowerThresholdMin,
		fiNvlinkPowerThresholdMax, fiNvlinkPowerThresholdSupported,
		fiNvlinkGetPowerState, fiNvlinkGetPowerThreshold,
		fiNvlinkRemoteNvlinkID, fiNvswitchConnectedLinkCount,
	} {
		if vt, _, ret := cd.GetNvLinkFieldValue(fid, 0); ret != nvml.ERROR_NOT_SUPPORTED || vt != NVLinkFieldUnsupported {
			t.Errorf("field %d (no nvlink) = (type=%d,ret=%v), want (Unsupported,NOT_SUPPORTED)", fid, vt, ret)
		}
	}
}
