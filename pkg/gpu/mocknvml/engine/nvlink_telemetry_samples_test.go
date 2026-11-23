// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// rubinSwitchFabricDevice builds device 0 of a two-GPU switch fabric with the
// given architecture, a per-device bw_mode block and fabric info, which is
// everything the Rubin-gated NVLink calls look at.
//
// No t.Parallel() in its callers: Init()/Shutdown() drive engine-wide state.
func rubinSwitchFabricDevice(t *testing.T, arch string) *ConfigurableDevice {
	t.Helper()
	yaml := &YAMLConfig{
		System: SystemConfig{DriverVersion: "600.00", NumDevices: 2},
		DeviceDefaults: DeviceConfig{
			Architecture: arch,
			Fabric:       &FabricConfig{State: "completed"},
		},
		NVLink: &NVLinkConfig{
			Version:              5,
			LinksPerGPU:          4,
			BandwidthPerLinkMbps: 53000,
			Switches:             []NVSwitchConfig{{BDF: "0000:01:00.0"}},
			BwMode:               &NVLinkBwModeConfig{Scope: NVLinkBwModeScopeDevice},
		},
	}
	cfg := &Config{NumDevices: 2, DriverVersion: "600.00", YAMLConfig: yaml}
	e := NewEngine(cfg)
	require.Equal(t, nvml.SUCCESS, e.Init(), "engine init")
	t.Cleanup(func() { _ = e.Shutdown() })

	handle, _ := e.DeviceGetHandleByIndex(0)
	cd, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok, "expected ConfigurableDevice")
	return cd
}

func telemetryReq(link uint32, typ nvml.NvlinkTelemetrySampleType, count uint32) NvLinkTelemetryRequest {
	return NvLinkTelemetryRequest{Link: link, SampleType: typ, Count: count, HasBuffer: count > 0}
}

func TestGetMockNvLinkTelemetrySamples_PreRubinNotSupported(t *testing.T) {
	for _, arch := range []string{"hopper", "blackwell"} {
		t.Run(arch, func(t *testing.T) {
			dev := rubinSwitchFabricDevice(t, arch)
			_, ret := dev.GetMockNvLinkTelemetrySamples(
				[]NvLinkTelemetryRequest{telemetryReq(0, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, 1)})
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
		})
	}
}

// TestGetMockNvLinkTelemetrySamples_History pins the sample series: oldest
// first, 100 ms apart, rising, and ending at the value the THROUGHPUT_RAW
// field reports, so the two views of one counter agree.
func TestGetMockNvLinkTelemetrySamples_History(t *testing.T) {
	dev := rubinSwitchFabricDevice(t, "rubin")
	fixed := dev.fabric.epoch.Add(time.Hour).Truncate(nvlinkTelemetrySampleInterval)
	dev.fabric.now = func() time.Time { return fixed }

	results, ret := dev.GetMockNvLinkTelemetrySamples([]NvLinkTelemetryRequest{
		telemetryReq(0, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, 20),
		telemetryReq(1, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_RX, 5),
	})
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, results, 2)

	tx := results[0]
	require.Equal(t, nvml.SUCCESS, tx.Return)
	require.Equal(t, uint32(20), tx.Count)
	require.Len(t, tx.Samples, 20)
	for i := 1; i < len(tx.Samples); i++ {
		require.Greater(t, tx.Samples[i], tx.Samples[i-1], "sample %d must exceed its predecessor", i)
	}
	_, fieldTx, _ := dev.GetNvLinkFieldValue(fiNvlinkThroughputRawTx, 0)
	require.Equal(t, fieldTx, tx.Samples[len(tx.Samples)-1], "newest TX sample is the field value")

	rx := results[1]
	require.Equal(t, nvml.SUCCESS, rx.Return)
	require.Len(t, rx.Samples, 5)
	_, fieldRx, _ := dev.GetNvLinkFieldValue(fiNvlinkThroughputRawRx, 1)
	require.Equal(t, fieldRx, rx.Samples[len(rx.Samples)-1], "newest RX sample is the field value")
}

func TestGetMockNvLinkTelemetrySamples_CountQuery(t *testing.T) {
	dev := rubinSwitchFabricDevice(t, "rubin")

	results, ret := dev.GetMockNvLinkTelemetrySamples([]NvLinkTelemetryRequest{
		{Link: 0, SampleType: nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX},
	})
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.SUCCESS, results[0].Return)
	require.Equal(t, uint32(nvlinkTelemetryMaxSamples), results[0].Count, "count 0 reports the supported depth")
	require.Empty(t, results[0].Samples)
}

// TestGetMockNvLinkTelemetrySamples_InvalidSlot pins that one bad slot fails
// the batch and that only that slot is marked, so a caller can find it.
func TestGetMockNvLinkTelemetrySamples_InvalidSlot(t *testing.T) {
	dev := rubinSwitchFabricDevice(t, "rubin")
	good := telemetryReq(0, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, 1)

	for name, bad := range map[string]NvLinkTelemetryRequest{
		"bad sample type":  telemetryReq(0, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_COUNT, 1),
		"link out of range": telemetryReq(uint32(dev.fabric.LinkBound()), nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, 1),
		"count without buffer": {
			Link: 0, SampleType: nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, Count: 1,
		},
		"count above max": telemetryReq(0, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, nvlinkTelemetryMaxSamples+1),
	} {
		t.Run(name, func(t *testing.T) {
			results, ret := dev.GetMockNvLinkTelemetrySamples([]NvLinkTelemetryRequest{good, bad})
			require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "batch return")
			require.Equal(t, nvml.SUCCESS, results[0].Return, "valid slot is not blamed")
			require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, results[1].Return, "offending slot is marked")
		})
	}
}

func TestGetMockNvLinkTelemetrySamples_InactiveLink(t *testing.T) {
	dev := rubinSwitchFabricDevice(t, "rubin")

	results, ret := dev.GetMockNvLinkTelemetrySamples([]NvLinkTelemetryRequest{
		telemetryReq(0, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, 1),
		telemetryReq(uint32(dev.fabric.LinkBound()-1), nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, 1),
	})
	require.Equal(t, nvml.SUCCESS, ret, "an inactive link does not fail the batch")
	require.Equal(t, nvml.SUCCESS, results[0].Return)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, results[1].Return, "the last link is not up on a 4-link board")
}

// TestRubinNvlinkCalls_Vrnvl72 pins that the shipped Rubin profile answers
// both Rubin-only NVLink calls, which the configuration docs promise.
func TestRubinNvlinkCalls_Vrnvl72(t *testing.T) {
	persistSetterWrites(t)
	yc, err := LoadYAMLConfig(filepath.Join("..", "configs", "mock-nvml-config-vrnvl72.yaml"))
	require.NoError(t, err)
	e := NewEngine(&Config{NumDevices: len(yc.Devices), YAMLConfig: yc})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	dev := e.LookupConfigurableDevice(handle)

	results, ret := dev.GetMockNvLinkTelemetrySamples(
		[]NvLinkTelemetryRequest{telemetryReq(0, nvml.NVLINK_TELEMETRY_SAMPLE_TYPE_THROUGHPUT_RAW_TX, 4)})
	require.Equal(t, nvml.SUCCESS, ret, "telemetry")
	require.Equal(t, nvml.SUCCESS, results[0].Return, "link 0 is up")

	_, ret = dev.SetMockNvlinkBwModeAsync(0, true)
	require.Equal(t, nvml.SUCCESS, ret, "async bandwidth-mode set")
}
