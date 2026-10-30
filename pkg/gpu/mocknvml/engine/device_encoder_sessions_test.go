// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestGetEncoderSessions_FollowEncoderStats(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		EncoderStats: &EncoderStatsConfig{SessionCount: 3, AverageFPS: 60, AverageLatencyUS: 4200},
		Processes:    []ProcessConfig{{PID: 100}, {PID: 200}},
	})

	sessions, ret := dev.GetEncoderSessions()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, sessions, 3)

	// Sessions are spread over the processes in order, so every pid that the
	// process list reports can be matched to the sessions it owns.
	require.Equal(t, []uint32{100, 200, 100}, []uint32{sessions[0].Pid, sessions[1].Pid, sessions[2].Pid})
	for i, s := range sessions {
		require.Equal(t, uint32(i+1), s.SessionId)
		require.Equal(t, uint32(60), s.AverageFps)
		require.Equal(t, uint32(4200), s.AverageLatency)
	}
}

func TestGetEncoderSessions_NoneConfigured(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	sessions, ret := dev.GetEncoderSessions()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, sessions)
}

func TestGetEncoderSessions_WithoutProcessesAreUnattributed(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{EncoderStats: &EncoderStatsConfig{SessionCount: 2}})

	sessions, ret := dev.GetEncoderSessions()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, sessions, 2)
	require.Zero(t, sessions[0].Pid)
}

func TestGetFBCSessions_FollowFBCStats(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		FBCStats:  &FBCStatsConfig{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 900},
		Processes: []ProcessConfig{{PID: 7}},
	})

	sessions, ret := dev.GetFBCSessions()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, sessions, 2)
	for i, s := range sessions {
		require.Equal(t, uint32(i+1), s.SessionId)
		require.Equal(t, uint32(7), s.Pid)
		require.Equal(t, uint32(30), s.AverageFPS)
		require.Equal(t, uint32(900), s.AverageLatency)
	}
}

func TestGetEncoderCapacity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		procs []ProcessConfig
		typ   nvml.EncoderType
		want  int
		ret   nvml.Return
	}{
		{name: "idle GPU has all of it", typ: nvml.ENCODER_QUERY_H264, want: 100, ret: nvml.SUCCESS},
		{
			name:  "encoder utilization is subtracted",
			procs: []ProcessConfig{{PID: 1, EncUtil: 30}, {PID: 2, EncUtil: 25}},
			typ:   nvml.ENCODER_QUERY_HEVC, want: 45, ret: nvml.SUCCESS,
		},
		{
			name:  "saturated encoder bottoms out at zero",
			procs: []ProcessConfig{{PID: 1, EncUtil: 80}, {PID: 2, EncUtil: 80}},
			typ:   nvml.ENCODER_QUERY_AV1, want: 0, ret: nvml.SUCCESS,
		},
		{name: "unknown codec is rejected", typ: nvml.EncoderType(99), ret: nvml.ERROR_INVALID_ARGUMENT},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, &DeviceConfig{Processes: tt.procs})

			got, ret := dev.GetEncoderCapacity(tt.typ)
			require.Equal(t, tt.ret, ret)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetMultiGpuBoard(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  DeviceConfig
		want int
	}{
		{name: "defaults to a single-GPU board", cfg: DeviceConfig{}, want: 0},
		{name: "profile marks a dual-GPU board", cfg: DeviceConfig{MultiGPUBoard: true}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, &tt.cfg)

			got, ret := dev.GetMultiGpuBoard()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetGpuOperationMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		cfg         *GPUOperationModeConfig
		wantCurrent nvml.GpuOperationMode
		wantPending nvml.GpuOperationMode
	}{
		{name: "defaults to all_on", wantCurrent: nvml.GOM_ALL_ON, wantPending: nvml.GOM_ALL_ON},
		{
			name:        "current and pending are independent",
			cfg:         &GPUOperationModeConfig{Current: "compute", Pending: "low_dp"},
			wantCurrent: nvml.GOM_COMPUTE, wantPending: nvml.GOM_LOW_DP,
		},
		{
			name:        "pending follows current when unset",
			cfg:         &GPUOperationModeConfig{Current: "compute"},
			wantCurrent: nvml.GOM_COMPUTE, wantPending: nvml.GOM_COMPUTE,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, &DeviceConfig{GPUOperationMode: tt.cfg})

			current, pending, ret := dev.GetGpuOperationMode()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tt.wantCurrent, current)
			require.Equal(t, tt.wantPending, pending)
		})
	}
}

func TestValidateYAMLConfig_RejectsUnknownGPUOperationMode(t *testing.T) {
	t.Parallel()
	cfg := &YAMLConfig{DeviceDefaults: DeviceConfig{GPUOperationMode: &GPUOperationModeConfig{Current: "turbo"}}}

	err := validateGPUOperationModes(cfg)
	require.ErrorContains(t, err, "gpu_operation_mode.current")
	require.ErrorContains(t, err, "turbo")
}

// A per-device block must reach the device that declares it and stay off its
// siblings: with the merge missing, the getters answer the defaults' constants,
// which is indistinguishable from a profile that never set the field.
func TestGetDeviceConfigMergesPerDeviceSessionAndBoardFields(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		NumDevices: 2,
		YAMLConfig: &YAMLConfig{
			Devices: []DeviceOverride{{
				Index: 1,
				DeviceConfig: DeviceConfig{
					EncoderStats:     &EncoderStatsConfig{SessionCount: 3},
					FBCStats:         &FBCStatsConfig{SessionCount: 2},
					GPUOperationMode: &GPUOperationModeConfig{Current: "compute"},
					MultiGPUBoard:    true,
				},
			}},
		},
	}

	got := cfg.GetDeviceConfig(1)
	require.EqualValues(t, 3, got.EncoderStats.SessionCount)
	require.EqualValues(t, 2, got.FBCStats.SessionCount)
	require.Equal(t, "compute", got.GPUOperationMode.Current)
	require.True(t, got.MultiGPUBoard)

	sibling := cfg.GetDeviceConfig(0)
	require.Nil(t, sibling.EncoderStats)
	require.Nil(t, sibling.FBCStats)
	require.Nil(t, sibling.GPUOperationMode)
	require.False(t, sibling.MultiGPUBoard)
}
