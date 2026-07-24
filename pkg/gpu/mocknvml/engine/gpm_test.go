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

func boolPtr(b bool) *bool { return &b }

func TestGetGpmSupport_ArchitectureDefault(t *testing.T) {
	tests := []struct {
		arch     string
		expected uint32
	}{
		{"ampere", 0},
		{"ada", 0},
		{"hopper", 1},
		{"blackwell", 1},
		{"", 0}, // unknown architecture
	}
	for _, tt := range tests {
		dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: tt.arch})
		supported, ret := dev.GetGpmSupport()
		require.Equal(t, nvml.SUCCESS, ret, "arch %q", tt.arch)
		require.Equal(t, tt.expected, supported, "arch %q", tt.arch)
	}
}

func TestGetGpmSupport_ConfigOverride(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "ampere",
		GPM:          &GPMConfig{Supported: boolPtr(true)},
	})
	supported, ret := dev.GetGpmSupport()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), supported)

	dev = newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		GPM:          &GPMConfig{Supported: boolPtr(false)},
	})
	supported, ret = dev.GetGpmSupport()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, supported)
}

func TestGpmSampleLifecycle(t *testing.T) {
	key := GpmSampleAlloc()
	require.NotZero(t, key)
	require.True(t, GpmSampleFree(key))
	require.False(t, GpmSampleFree(key), "double free must fail")

	// Snapshot into a freed key must fail.
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Utilization:  &UtilizationConfig{GPU: 60, Memory: 40},
	})
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.GpmSnapshotInto(key))
}

func TestGpmSnapshotInto_UnsupportedArch(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "ampere"})
	key := GpmSampleAlloc()
	defer GpmSampleFree(key)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.GpmSnapshotInto(key))
}

func TestGpmMetricsGet_ActivityFromUtilization(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Utilization:  &UtilizationConfig{GPU: 60, Memory: 40},
	})

	k1, k2 := GpmSampleAlloc(), GpmSampleAlloc()
	defer GpmSampleFree(k1)
	defer GpmSampleFree(k2)
	require.Equal(t, nvml.SUCCESS, dev.GpmSnapshotInto(k1))
	require.Equal(t, nvml.SUCCESS, dev.GpmSnapshotInto(k2))

	tests := []struct {
		name     string
		id       uint32
		expected float64
	}{
		{"GRAPHICS_UTIL", gpmMetricGraphicsUtil, 60.0},
		{"SM_UTIL", gpmMetricSMUtil, 60.0},
		{"SM_OCCUPANCY", gpmMetricSMOccupancy, 60.0 * gpmRatioSMOccupancy},
		{"INTEGER_UTIL", gpmMetricIntegerUtil, 60.0 * gpmRatioInteger},
		{"ANY_TENSOR", gpmMetricAnyTensorUtil, 60.0 * gpmRatioAnyTensor},
		{"DFMA_TENSOR", gpmMetricDFMATensorUtil, 60.0 * gpmRatioDFMA},
		{"HMMA_TENSOR", gpmMetricHMMATensorUtil, 60.0 * gpmRatioHMMA},
		{"DMMA_TENSOR", gpmMetricDMMATensorUtil, 60.0 * gpmRatioDMMA},
		{"IMMA_TENSOR", gpmMetricIMMATensorUtil, 60.0 * gpmRatioIMMA},
		{"DRAM_BW", gpmMetricDramBwUtil, 40.0},
		{"FP64", gpmMetricFP64Util, 60.0 * gpmRatioFP64},
		{"FP32", gpmMetricFP32Util, 60.0 * gpmRatioFP32},
		{"FP16", gpmMetricFP16Util, 60.0 * gpmRatioFP16},
		{"PCIE_TX", gpmMetricPcieTxPerSec, 0.6 * gpmDefaultPcieMiBPerSec},
		{"PCIE_RX", gpmMetricPcieRxPerSec, 0.6 * gpmDefaultPcieMiBPerSec},
	}
	ids := make([]uint32, len(tests))
	for i, tt := range tests {
		ids[i] = tt.id
	}
	values, rets, ret := GpmMetricsGet(k1, k2, ids)
	require.Equal(t, nvml.SUCCESS, ret)
	for i, tt := range tests {
		require.Equal(t, nvml.SUCCESS, rets[i], "%s (id %d)", tt.name, tt.id)
		require.Equal(t, tt.expected, values[i], tt.name)
	}

	// Sample order must not matter: DCGM passes (older, newer) but nothing
	// guarantees it.
	swapped, _, ret := GpmMetricsGet(k2, k1, ids)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, values, swapped, "reversed sample order must yield identical results")
}

func TestGpmMetricsGet_PcieConfigOverride(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Utilization:  &UtilizationConfig{GPU: 50},
		GPM:          &GPMConfig{PcieTxMiBPerSec: 4096, PcieRxMiBPerSec: 1024},
	})
	k1, k2 := GpmSampleAlloc(), GpmSampleAlloc()
	defer GpmSampleFree(k1)
	defer GpmSampleFree(k2)
	require.Equal(t, nvml.SUCCESS, dev.GpmSnapshotInto(k1))
	require.Equal(t, nvml.SUCCESS, dev.GpmSnapshotInto(k2))

	values, rets, ret := GpmMetricsGet(k1, k2, []uint32{gpmMetricPcieTxPerSec, gpmMetricPcieRxPerSec})
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.SUCCESS, rets[0])
	require.Equal(t, nvml.SUCCESS, rets[1])
	require.Equal(t, 0.5*4096, values[0], "PCIE_TX must use configured rate")
	require.Equal(t, 0.5*1024, values[1], "PCIE_RX must use configured rate")
}

// newGpmFabricDevice builds an 8-GPU switch-fabric device (18 links via
// switch-link auto-expansion, like newSwitchFabricDevice) that also reports a
// Hopper architecture so the GPM path is enabled.
func newGpmFabricDevice(t *testing.T) *ConfigurableDevice {
	t.Helper()
	yaml := &YAMLConfig{
		System:         SystemConfig{DriverVersion: "560.35.03", NumDevices: 8},
		DeviceDefaults: DeviceConfig{Architecture: "hopper"},
		NVLink: &NVLinkConfig{
			Version:              5,
			LinksPerGPU:          18,
			BandwidthPerLinkMbps: 53000,
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
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })

	handle, _ := e.DeviceGetHandleByIndex(0)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok, "expected ConfigurableDevice")
	return cd
}

func TestGpmMetricsGet_NvLinkRates(t *testing.T) {
	cd := newGpmFabricDevice(t)

	// Every link accrues at the same configured rate, so equal per-link
	// values could hide an id->link mapping bug. Deactivate L1: its rate
	// must read 0 while L0 stays positive — that asymmetry pins ids 62/63
	// to link 0 and 64 to link 1.
	for i := range cd.fabric.links[cd.index] {
		if cd.fabric.links[cd.index][i].Link == 1 {
			cd.fabric.links[cd.index][i].Active = false
		}
	}

	// Freeze the fabric clock (the snapshot clock — see GpmSnapshotInto) at
	// two points 10s apart so counter deltas and dt are both deterministic.
	t0 := cd.fabric.epoch.Add(time.Hour)
	t1 := t0.Add(10 * time.Second)
	dt := t1.Sub(t0).Seconds()

	k1, k2 := GpmSampleAlloc(), GpmSampleAlloc()
	defer GpmSampleFree(k1)
	defer GpmSampleFree(k2)
	cd.fabric.now = func() time.Time { return t0 }
	require.Equal(t, nvml.SUCCESS, cd.GpmSnapshotInto(k1))
	cd.fabric.now = func() time.Time { return t1 }
	require.Equal(t, nvml.SUCCESS, cd.GpmSnapshotInto(k2))

	// Expected per-link rates straight from the counter source of truth.
	perLink := make([]float64, nvLinkMaxLinks)
	var total float64
	for link := 0; link < nvLinkMaxLinks; link++ {
		rx0, _ := cd.fabric.NvLinkCounters(cd.index, link, t0)
		rx1, _ := cd.fabric.NvLinkCounters(cd.index, link, t1)
		perLink[link] = float64(rx1-rx0) / dt / (1024 * 1024)
		total += perLink[link]
	}
	require.Positive(t, total, "fabric counters must accrue over the window")

	ids := []uint32{
		gpmMetricNvlinkTotalRxPerSec,   // 60
		gpmMetricNvlinkTotalTxPerSec,   // 61
		gpmMetricNvlinkL0RxPerSec,      // 62 -> L0 RX
		gpmMetricNvlinkL0RxPerSec + 1,  // 63 -> L0 TX
		gpmMetricNvlinkL0RxPerSec + 2,  // 64 -> L1 RX
		gpmMetricNvlinkL17TxPerSec,     // 97 -> L17 TX
		gpmMetricNvlinkL17TxPerSec - 1, // 96 -> L17 RX
	}
	values, rets, ret := GpmMetricsGet(k1, k2, ids)
	require.Equal(t, nvml.SUCCESS, ret)
	for i, r := range rets {
		require.Equal(t, nvml.SUCCESS, r, "metric id %d", ids[i])
	}

	// The mock accrues rx == tx per link, so totals match; the deactivated
	// L1 gives the asymmetry that verifies the (id-62)/2 link mapping.
	require.InDelta(t, total, values[0], 1e-9, "TOTAL_RX")
	require.InDelta(t, total, values[1], 1e-9, "TOTAL_TX")
	require.Positive(t, perLink[0], "L0 must accrue")
	require.InDelta(t, perLink[0], values[2], 1e-9, "L0 RX (id 62)")
	require.InDelta(t, perLink[0], values[3], 1e-9, "L0 TX (id 63)")
	require.Zero(t, perLink[1], "deactivated L1 must not accrue")
	require.Zero(t, values[4], "L1 RX (id 64) must be 0 for the deactivated link")
	require.Positive(t, values[5], "L17 TX (id 97)")
	require.InDelta(t, perLink[17], values[5], 1e-9, "L17 TX (id 97)")
	require.InDelta(t, perLink[17], values[6], 1e-9, "L17 RX (id 96)")

	// Zero elapsed time must not divide by zero: same clock for both samples.
	k3 := GpmSampleAlloc()
	defer GpmSampleFree(k3)
	require.Equal(t, nvml.SUCCESS, cd.GpmSnapshotInto(k3))
	values, rets, ret = GpmMetricsGet(k2, k3, []uint32{gpmMetricNvlinkTotalRxPerSec})
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.SUCCESS, rets[0])
	require.Zero(t, values[0], "dt == 0 must yield a zero rate, not NaN/Inf")
}

func TestGpmMetricsGet_UnknownMetricPerEntryError(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		Utilization:  &UtilizationConfig{GPU: 10},
	})
	k1, k2 := GpmSampleAlloc(), GpmSampleAlloc()
	defer GpmSampleFree(k1)
	defer GpmSampleFree(k2)
	require.Equal(t, nvml.SUCCESS, dev.GpmSnapshotInto(k1))
	require.Equal(t, nvml.SUCCESS, dev.GpmSnapshotInto(k2))

	// Metric 250 (SM_CYCLES et al.) is not modeled: the call succeeds, the
	// entry reports NOT_SUPPORTED — matching real NVML per-metric semantics.
	values, rets, ret := GpmMetricsGet(k1, k2, []uint32{gpmMetricSMUtil, 250})
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.SUCCESS, rets[0])
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, rets[1])
	require.Zero(t, values[1])
}

func TestGpmMetricsGet_InvalidSamples(t *testing.T) {
	k := GpmSampleAlloc()
	defer GpmSampleFree(k)
	// Never-snapshotted and unknown keys are both invalid.
	_, _, ret := GpmMetricsGet(k, k, []uint32{gpmMetricSMUtil})
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
	_, _, ret = GpmMetricsGet(999999999, k, []uint32{gpmMetricSMUtil})
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}
