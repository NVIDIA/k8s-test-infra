// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Encoder and FBC sessions, encoder capacity, the multi-GPU board flag and the
// GPU operation mode driven through the built library's C ABI.
//
// The two session getters fill a caller-allocated array of structs, so only a
// call across the real C boundary shows whether each field lands at the right
// offset: a bridge that wrote a field one slot off would still report SUCCESS
// and the right count. Device 4 of the fixture is the one that models sessions.

package main

import (
	"fmt"
	"os"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const sessionsFixtureDevice = 4

func testSessionsAndBoard(deviceCount int) []testResult {
	const name = "sessions"
	if os.Getenv("MOCK_NVML_CONFIG") == "" || deviceCount <= sessionsFixtureDevice {
		return []testResult{skippedResult(name, "needs the fixture config, which models sessions on device 4")}
	}
	device, ret := nvml.DeviceGetHandleByIndex(sessionsFixtureDevice)
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetHandleByIndex failed: %v", nvml.ErrorString(ret))}}
	}

	var results []testResult
	check := func(label string, ok bool, format string, args ...any) {
		results = append(results, testResult{name + "/" + label, ok, fmt.Sprintf(format, args...)})
	}

	enc, ret := device.GetEncoderSessions()
	check("encoder_sessions", ret == nvml.SUCCESS && len(enc) == 3, "ret=%v len=%d, want 3", ret, len(enc))
	// Owners alternate over the two configured processes.
	wantPids := []uint32{4242, 4343, 4242}
	for i, s := range enc {
		ok := s.SessionId == uint32(i+1) && s.Pid == wantPids[i] && s.AverageFps == 60 &&
			s.AverageLatency == 4200 && s.HResolution == 1920 && s.VResolution == 1080
		check(fmt.Sprintf("encoder_session_%d", i), ok, "%+v", s)
	}

	fbc, ret := device.GetFBCSessions()
	check("fbc_sessions", ret == nvml.SUCCESS && len(fbc) == 2, "ret=%v len=%d, want 2", ret, len(fbc))
	for i, s := range fbc {
		ok := s.SessionId == uint32(i+1) && s.Pid == wantPids[i] && s.AverageFPS == 30 &&
			s.AverageLatency == 900 && s.HResolution == 1920 && s.VResolution == 1080 &&
			s.HMaxResolution == 1920 && s.VMaxResolution == 1080
		check(fmt.Sprintf("fbc_session_%d", i), ok, "%+v", s)
	}

	for _, codec := range []nvml.EncoderType{nvml.ENCODER_QUERY_H264, nvml.ENCODER_QUERY_HEVC, nvml.ENCODER_QUERY_AV1} {
		capacity, ret := device.GetEncoderCapacity(codec)
		check(fmt.Sprintf("encoder_capacity_%d", codec), ret == nvml.SUCCESS && capacity == 45,
			"ret=%v capacity=%d, want 45", ret, capacity)
	}
	_, ret = device.GetEncoderCapacity(nvml.EncoderType(99))
	check("encoder_capacity_unknown_codec", ret == nvml.ERROR_INVALID_ARGUMENT, "ret=%v", ret)

	multi, ret := device.GetMultiGpuBoard()
	check("multi_gpu_board", ret == nvml.SUCCESS && multi == 1, "ret=%v multi=%d, want 1", ret, multi)

	current, pending, ret := device.GetGpuOperationMode()
	check("gpu_operation_mode", ret == nvml.SUCCESS && current == nvml.GOM_COMPUTE && pending == nvml.GOM_LOW_DP,
		"ret=%v current=%d pending=%d, want compute/low_dp", ret, current, pending)

	// A device that models none of this keeps the defaults.
	plain, ret := nvml.DeviceGetHandleByIndex(0)
	if ret == nvml.SUCCESS {
		sessions, sret := plain.GetEncoderSessions()
		multi, mret := plain.GetMultiGpuBoard()
		check("defaults_on_plain_device", sret == nvml.SUCCESS && len(sessions) == 0 && mret == nvml.SUCCESS && multi == 0,
			"sessions=%d multi=%d", len(sessions), multi)
	}
	return results
}
