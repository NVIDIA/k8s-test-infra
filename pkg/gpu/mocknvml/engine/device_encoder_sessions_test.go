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

// The empty answer only means "nothing configured" if the same getter answers
// sessions once a profile configures them; a getter that always returns an
// empty list must fail here too.
func TestGetEncoderSessions_NoneConfigured(t *testing.T) {
	t.Parallel()
	configured := newTestDeviceWithConfig(t, &DeviceConfig{EncoderStats: &EncoderStatsConfig{SessionCount: 1}})
	sessions, ret := configured.GetEncoderSessions()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, sessions, 1)

	dev := newTestDeviceWithConfig(t, &DeviceConfig{})
	sessions, ret = dev.GetEncoderSessions()
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
			if tt.ret != nvml.SUCCESS {
				return
			}
			// Utilization is what capacity leaves out, so the two add up to 100.
			util, _, ret := dev.GetEncoderUtilization()
			require.Equal(t, nvml.SUCCESS, ret)
			require.EqualValues(t, 100-tt.want, util)
		})
	}
}

// The sessions and the capacity are carved out of the processes, which a lost
// GPU no longer reports; listing them would describe work on a GPU that is gone.
func TestEncoderGetters_LostDevice(t *testing.T) {
	t.Parallel()
	cfg := &DeviceConfig{
		EncoderStats: &EncoderStatsConfig{SessionCount: 2},
		FBCStats:     &FBCStatsConfig{SessionCount: 2},
		Processes:    []ProcessConfig{{PID: 100, EncUtil: 30}},
	}
	healthy := newTestDeviceWithConfig(t, cfg)
	sessions, ret := healthy.GetEncoderSessions()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, sessions, 2)

	lost := *cfg
	lost.Failure = &FailureInjectionConfig{Mode: FailureModeLost}
	dev := newTestDeviceWithConfig(t, &lost)
	// A guarded getter is what trips the injector, as on a node where a
	// monitoring agent's first query is the one that finds the GPU gone; the
	// session getters only observe that state, as the process list does.
	_, ret = dev.GetMemoryInfo()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)

	encSessions, ret := dev.GetEncoderSessions()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
	require.Empty(t, encSessions)
	fbcSessions, ret := dev.GetFBCSessions()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
	require.Empty(t, fbcSessions)
	_, ret = dev.GetEncoderCapacity(nvml.ENCODER_QUERY_H264)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
}

// Two GPUs the profile puts on one board report the same board ID, call each
// other on the same board and both say they sit on a multi-GPU board; a third
// GPU keeps its PCI-derived board of its own. The board ID being shared must
// not leak into the per-GPU ID the CPER log and system events name a GPU by.
func TestGetMultiGpuBoard_FollowsSharedBoardID(t *testing.T) {
	t.Parallel()
	shared := new(HexID(0x4100))
	e := pcieIdentityEngine(t, DeviceConfig{},
		DeviceOverride{Index: 0, DeviceConfig: DeviceConfig{BoardID: shared}},
		DeviceOverride{Index: 1, DeviceConfig: DeviceConfig{BoardID: shared}},
		DeviceOverride{Index: 2},
	)
	gpu0, gpu1, gpu2 := pcieIdentityDevice(t, e, 0), pcieIdentityDevice(t, e, 1), pcieIdentityDevice(t, e, 2)

	for _, tc := range []struct {
		name  string
		dev   *ConfigurableDevice
		multi int
	}{{"gpu0", gpu0, 1}, {"gpu1", gpu1, 1}, {"gpu2", gpu2, 0}} {
		multi, ret := tc.dev.GetMultiGpuBoard()
		require.Equal(t, nvml.SUCCESS, ret, tc.name)
		require.Equal(t, tc.multi, multi, tc.name)
	}

	id0, ret := gpu0.GetBoardId()
	require.Equal(t, nvml.SUCCESS, ret)
	require.EqualValues(t, 0x4100, id0)
	id2, ret := gpu2.GetBoardId()
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotEqualValues(t, 0x4100, id2)

	same, ret := gpu0.OnSameBoard(gpu1)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 1, same)
	same, ret = gpu0.OnSameBoard(gpu2)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, same)

	require.NotEqual(t, gpu0.gpuID, gpu1.gpuID, "board-mates must keep distinct GPU IDs")
}

// A GPU with no board_id is a board of its own. The 0 only means that if the
// same getter answers 1 once a profile puts GPUs on one board, so a getter
// that always answers 0 fails here too.
func TestGetMultiGpuBoard_DefaultsToABoardOfItsOwn(t *testing.T) {
	t.Parallel()
	shared := pcieIdentityEngine(t, DeviceConfig{BoardID: new(HexID(0x4100))},
		DeviceOverride{Index: 0}, DeviceOverride{Index: 1})
	own := pcieIdentityEngine(t, DeviceConfig{}, DeviceOverride{Index: 0}, DeviceOverride{Index: 1})
	for i := range 2 {
		multi, ret := pcieIdentityDevice(t, shared, i).GetMultiGpuBoard()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, 1, multi, "gpu%d on the shared board", i)

		multi, ret = pcieIdentityDevice(t, own, i).GetMultiGpuBoard()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Zero(t, multi, "gpu%d on its own board", i)
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

// With both fields wrong the error must name the same one every time, so a
// profile author sees a stable message; map iteration would pick at random.
func TestValidateYAMLConfig_GPUOperationModeErrorIsDeterministic(t *testing.T) {
	t.Parallel()
	cfg := &YAMLConfig{DeviceDefaults: DeviceConfig{GPUOperationMode: &GPUOperationModeConfig{Current: "turbo", Pending: "eco"}}}

	for range 50 {
		err := validateGPUOperationModes(cfg)
		require.ErrorContains(t, err, "gpu_operation_mode.current")
		require.ErrorContains(t, err, "turbo")
	}
}

func TestValidateYAMLConfig_RejectsBoardIDWiderThan32Bits(t *testing.T) {
	t.Parallel()
	ok := &YAMLConfig{Devices: []DeviceOverride{{Index: 0, DeviceConfig: DeviceConfig{BoardID: new(HexID(0xffffffff))}}}}
	require.NoError(t, validateBoardIDs(ok))

	wide := &YAMLConfig{Devices: []DeviceOverride{{Index: 3, DeviceConfig: DeviceConfig{BoardID: new(HexID(0x100000000))}}}}
	err := validateBoardIDs(wide)
	require.ErrorContains(t, err, "devices[index=3].board_id")
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
					BoardID:          new(HexID(0x4100)),
				},
			}},
		},
	}

	got := cfg.GetDeviceConfig(1)
	require.EqualValues(t, 3, got.EncoderStats.SessionCount)
	require.EqualValues(t, 2, got.FBCStats.SessionCount)
	require.Equal(t, "compute", got.GPUOperationMode.Current)
	require.EqualValues(t, 0x4100, *got.BoardID)

	sibling := cfg.GetDeviceConfig(0)
	require.Nil(t, sibling.EncoderStats)
	require.Nil(t, sibling.FBCStats)
	require.Nil(t, sibling.GPUOperationMode)
	require.Nil(t, sibling.BoardID)
}

// A device's board_id replaces the defaults' one, so a node whose GPUs share a
// board can still hold one that sits on its own.
func TestGetDeviceConfigAppliesPerDeviceBoardID(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		NumDevices: 2,
		YAMLConfig: &YAMLConfig{
			DeviceDefaults: DeviceConfig{BoardID: new(HexID(0x4100))},
			Devices: []DeviceOverride{{
				Index:        1,
				DeviceConfig: DeviceConfig{BoardID: new(HexID(0x4200))},
			}},
		},
	}

	require.EqualValues(t, 0x4100, *cfg.GetDeviceConfig(0).BoardID)
	require.EqualValues(t, 0x4200, *cfg.GetDeviceConfig(1).BoardID)
}
