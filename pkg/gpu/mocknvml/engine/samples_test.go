// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func samplesConfig() *DeviceConfig {
	return &DeviceConfig{
		Utilization: &UtilizationConfig{GPU: 42, Memory: 17, JPEG: 9, OFA: 4},
		Power:       &PowerConfig{CurrentDrawMW: 72000, MinLimitMW: 100, MaxLimitMW: 400000},
		Clocks:      &ClocksConfig{GraphicsCurrent: 1410, GraphicsMax: 1410, MemoryCurrent: 1215, MemoryMax: 1215},
	}
}

func sampleValue(s nvml.Sample) uint32 {
	return binary.NativeEndian.Uint32(s.SampleValue[:4])
}

// TestGetSamples_ReportsTheCurrentReadingOverTheBuffer keeps the samples
// consistent with the getter each type mirrors, so the history a consumer reads
// agrees with the reading nvidia-smi shows beside it.
func TestGetSamples_ReportsTheCurrentReadingOverTheBuffer(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, samplesConfig())

	for _, tc := range []struct {
		samplingType nvml.SamplingType
		want         uint32
	}{
		{nvml.TOTAL_POWER_SAMPLES, 72000},
		{nvml.GPU_UTILIZATION_SAMPLES, 42},
		{nvml.MEMORY_UTILIZATION_SAMPLES, 17},
		{nvml.ENC_UTILIZATION_SAMPLES, 0},
		{nvml.DEC_UTILIZATION_SAMPLES, 0},
		{nvml.PROCESSOR_CLK_SAMPLES, 1410},
		{nvml.MEMORY_CLK_SAMPLES, 1215},
		{nvml.JPG_UTILIZATION_SAMPLES, 9},
		{nvml.OFA_UTILIZATION_SAMPLES, 4},
	} {
		valueType, samples, ret := dev.GetSamples(tc.samplingType, 0)
		require.Equal(t, nvml.SUCCESS, ret, "type %d", tc.samplingType)
		require.Equal(t, nvml.VALUE_TYPE_UNSIGNED_INT, valueType, "type %d", tc.samplingType)
		require.Len(t, samples, sampleBufferLen, "lastSeen 0 asks for the whole buffer")
		for _, s := range samples {
			require.Equal(t, tc.want, sampleValue(s), "type %d", tc.samplingType)
		}
	}
}

func TestGetSamples_TimestampsRunOldestFirstOnTheSampleGrid(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, samplesConfig())

	_, samples, ret := dev.GetSamples(nvml.GPU_UTILIZATION_SAMPLES, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	interval := uint64(sampleInterval.Microseconds())
	for i := 1; i < len(samples); i++ {
		require.Equal(t, samples[i-1].TimeStamp+interval, samples[i].TimeStamp)
	}
	require.LessOrEqual(t, samples[len(samples)-1].TimeStamp, uint64(time.Now().UnixMicro()))
}

// TestGetSamples_LastSeenReturnsOnlyNewerSamples covers the polling contract:
// handing back the newest timestamp seen asks only for what came after it,
// and nothing having come after it is NOT_FOUND rather than an empty success.
func TestGetSamples_LastSeenReturnsOnlyNewerSamples(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, samplesConfig())

	_, all, ret := dev.GetSamples(nvml.GPU_UTILIZATION_SAMPLES, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	newest, previous := all[len(all)-1].TimeStamp, all[len(all)-2].TimeStamp

	_, after, ret := dev.GetSamples(nvml.GPU_UTILIZATION_SAMPLES, previous)
	if ret == nvml.SUCCESS && len(after) > 1 {
		t.Skip("the sample grid advanced between the two calls")
	}
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, after, 1)
	require.Equal(t, newest, after[0].TimeStamp)

	_, _, ret = dev.GetSamples(nvml.GPU_UTILIZATION_SAMPLES, uint64(time.Now().Add(time.Hour).UnixMicro()))
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

func TestGetSamples_RefusesWhatTheProfileDoesNotModel(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	_, _, ret := dev.GetSamples(nvml.TOTAL_POWER_SAMPLES, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "no power block")
	_, _, ret = dev.GetSamples(nvml.PROCESSOR_CLK_SAMPLES, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "no clocks block")
	_, _, ret = dev.GetSamples(nvml.MODULE_POWER_SAMPLES, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "module power is not modelled")
	_, _, ret = dev.GetSamples(nvml.SAMPLINGTYPE_COUNT, 0)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

// TestGetSamples_UtilizationUnavailableUnderMIG follows the header: on a
// MIG-enabled GPU the GPU, memory, encoder and decoder utilization samples are
// not supported, while power and clock samples still are.
func TestGetSamples_UtilizationUnavailableUnderMIG(t *testing.T) {
	t.Parallel()
	cfg := a100MIGConfig()
	cfg.Utilization = samplesConfig().Utilization
	cfg.Power = samplesConfig().Power
	dev := newTestDeviceWithConfig(t, cfg)
	enableMIG(t, dev)

	for _, st := range []nvml.SamplingType{
		nvml.GPU_UTILIZATION_SAMPLES, nvml.MEMORY_UTILIZATION_SAMPLES,
		nvml.ENC_UTILIZATION_SAMPLES, nvml.DEC_UTILIZATION_SAMPLES,
	} {
		_, _, ret := dev.GetSamples(st, 0)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "type %d", st)
	}
	_, _, ret := dev.GetSamples(nvml.TOTAL_POWER_SAMPLES, 0)
	require.Equal(t, nvml.SUCCESS, ret)
}
