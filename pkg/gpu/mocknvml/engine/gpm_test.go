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

func TestGetGpmSupport_ArchitectureDefault(t *testing.T) {
	tests := []struct {
		arch     string
		expected uint32
	}{
		{"ampere", 0},
		{"ada", 0},
		{"hopper", 1},
		{"blackwell", 1},
		// Unset: applyDeviceBaseOverrides skips the assignment, so the dgxa100
		// base default (Ampere) applies rather than UNKNOWN.
		{"", 0},
		// Unrecognized: parseArchitecture answers UNKNOWN, which is 0xFFFFFFFF
		// and must therefore clear no gate despite sorting above Blackwell.
		{"bogus_arch", 0},
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
		GPM:          &GPMConfig{Supported: new(true)},
	})
	supported, ret := dev.GetGpmSupport()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), supported)

	dev = newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "hopper",
		GPM:          &GPMConfig{Supported: new(false)},
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
		require.InDelta(t, tt.expected, values[i], 1e-9, tt.name)
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
	require.InDelta(t, 0.5*4096, values[0], 1e-9, "PCIE_TX must use configured rate")
	require.InDelta(t, 0.5*1024, values[1], 1e-9, "PCIE_RX must use configured rate")
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
	for link := range nvLinkMaxLinks {
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

// newGpmMigDevice is a MIG-enabled seven-slice board with GPM switched on and
// a fixed utilization, so per-instance metrics are deterministic.
func newGpmMigDevice(t *testing.T) *ConfigurableDevice {
	t.Helper()
	cfg := a100MIGConfig()
	cfg.GPM = &GPMConfig{Supported: new(true)}
	cfg.Utilization = &UtilizationConfig{GPU: 60, Memory: 40}
	dev := newTestDeviceWithConfig(t, cfg)
	enableMIG(t, dev)
	return dev
}

func createGpuInstance(t *testing.T, dev *ConfigurableDevice, profile int) uint32 {
	t.Helper()
	info, ret := dev.GetGpuInstanceProfileInfo(profile)
	require.Equal(t, nvml.SUCCESS, ret)
	gi, ret := dev.CreateGpuInstance(&info)
	require.Equal(t, nvml.SUCCESS, ret)
	giInfo, ret := gi.GetInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	return giInfo.Id
}

func gpmMigMetrics(t *testing.T, dev *ConfigurableDevice, giID uint32, ids []uint32) []float64 {
	t.Helper()
	k1, k2 := GpmSampleAlloc(), GpmSampleAlloc()
	defer GpmSampleFree(k1)
	defer GpmSampleFree(k2)
	require.Equal(t, nvml.SUCCESS, dev.GpmMigSnapshotInto(int(giID), k1))
	require.Equal(t, nvml.SUCCESS, dev.GpmMigSnapshotInto(int(giID), k2))
	values, rets, ret := GpmMetricsGet(k1, k2, ids)
	require.Equal(t, nvml.SUCCESS, ret)
	for i, r := range rets {
		require.Equal(t, nvml.SUCCESS, r, "metric id %d", ids[i])
	}
	return values
}

func TestGpmMigSnapshotInto_SplitsThroughputBySliceShare(t *testing.T) {
	dev := newGpmMigDevice(t)
	oneSlice := createGpuInstance(t, dev, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	threeSlice := createGpuInstance(t, dev, nvml.GPU_INSTANCE_PROFILE_3_SLICE)

	ids := []uint32{gpmMetricSMUtil, gpmMetricDramBwUtil, gpmMetricPcieTxPerSec, gpmMetricPcieRxPerSec}
	parentPcie := 0.6 * gpmDefaultPcieMiBPerSec

	// Activity is a percentage of the instance's own slices, so a uniformly
	// loaded board reports its utilization on every instance; throughput is
	// absolute and divides across the seven slices.
	small := gpmMigMetrics(t, dev, oneSlice, ids)
	require.InDelta(t, 60.0, small[0], 1e-9, "SM_UTIL 1g")
	require.InDelta(t, 40.0, small[1], 1e-9, "DRAM_BW 1g")
	require.InDelta(t, parentPcie/7, small[2], 1e-9, "PCIE_TX 1g")
	require.InDelta(t, parentPcie/7, small[3], 1e-9, "PCIE_RX 1g")

	large := gpmMigMetrics(t, dev, threeSlice, ids)
	require.InDelta(t, 60.0, large[0], 1e-9, "SM_UTIL 3g")
	require.InDelta(t, 40.0, large[1], 1e-9, "DRAM_BW 3g")
	require.InDelta(t, parentPcie*3/7, large[2], 1e-9, "PCIE_TX 3g")
	require.InDelta(t, parentPcie*3/7, large[3], 1e-9, "PCIE_RX 3g")
}

// A declared profile table need not include a whole-board row, so the board
// width comes from max_gpu_instances rather than the widest advertised profile.
func TestGpmMigSnapshotInto_ShareUsesBoardWidthNotWidestProfile(t *testing.T) {
	cfg := a100MIGConfig()
	cfg.GPM = &GPMConfig{Supported: new(true)}
	cfg.Utilization = &UtilizationConfig{GPU: 60, Memory: 40}
	var oneSliceOnly []MIGProfileSpec
	for _, spec := range cfg.MIG.SupportedProfiles {
		if spec.NVMLProfile == "1_SLICE" {
			oneSliceOnly = append(oneSliceOnly, spec)
		}
	}
	require.Len(t, oneSliceOnly, 1)
	cfg.MIG.SupportedProfiles = oneSliceOnly
	dev := newTestDeviceWithConfig(t, cfg)
	enableMIG(t, dev)

	giID := createGpuInstance(t, dev, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	values := gpmMigMetrics(t, dev, giID, []uint32{gpmMetricPcieTxPerSec})
	require.InDelta(t, 0.6*gpmDefaultPcieMiBPerSec/7, values[0], 1e-9)
}

func TestGpmMigSnapshotInto_Errors(t *testing.T) {
	key := GpmSampleAlloc()
	defer GpmSampleFree(key)

	dev := newGpmMigDevice(t)
	require.Equal(t, nvml.ERROR_NOT_FOUND, dev.GpmMigSnapshotInto(42, key), "no such GPU instance")

	disabled := a100MIGConfig()
	disabled.GPM = &GPMConfig{Supported: new(true)}
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED,
		newTestDeviceWithConfig(t, disabled).GpmMigSnapshotInto(1, key), "MIG disabled")

	noGpm := a100MIGConfig()
	noGpmDev := newTestDeviceWithConfig(t, noGpm)
	enableMIG(t, noGpmDev)
	giID := createGpuInstance(t, noGpmDev, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, noGpmDev.GpmMigSnapshotInto(int(giID), key), "GPM unsupported")
}

// A MIG device handle passed to nvmlGpmSampleGet names the same partition as
// its GPU instance ID passed to nvmlGpmMigSampleGet, so both must agree.
func TestGpmSnapshotInto_MigDeviceMatchesItsGpuInstance(t *testing.T) {
	dev := newGpmMigDevice(t)
	gi := createOneSliceGI(t, dev)
	createSpanningCI(t, gi)
	giInfo, ret := gi.GetInfo()
	require.Equal(t, nvml.SUCCESS, ret)

	migHandle, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	migDev, ok := migHandle.(*ConfigurableDevice)
	require.True(t, ok)

	ids := []uint32{gpmMetricSMUtil, gpmMetricPcieTxPerSec}
	k1, k2 := GpmSampleAlloc(), GpmSampleAlloc()
	defer GpmSampleFree(k1)
	defer GpmSampleFree(k2)
	require.Equal(t, nvml.SUCCESS, migDev.GpmSnapshotInto(k1))
	require.Equal(t, nvml.SUCCESS, migDev.GpmSnapshotInto(k2))
	viaDevice, _, ret := GpmMetricsGet(k1, k2, ids)
	require.Equal(t, nvml.SUCCESS, ret)

	require.Equal(t, gpmMigMetrics(t, dev, giInfo.Id, ids), viaDevice)
}

func TestGpmMetricValue_NvLinkRateScalesWithShare(t *testing.T) {
	s1 := &gpmSnapshot{taken: true, share: 0.5}
	s2 := &gpmSnapshot{taken: true, share: 0.5}
	s2.nvlinkRx[0] = 4 * 1024 * 1024
	s2.nvlinkTx[0] = 2 * 1024 * 1024

	rx, ret := gpmMetricValue(gpmMetricNvlinkTotalRxPerSec, s1, s2, 1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.InDelta(t, 2.0, rx, 1e-9)
	tx, ret := gpmMetricValue(gpmMetricNvlinkL0RxPerSec+1, s1, s2, 1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.InDelta(t, 1.0, tx, 1e-9)
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
