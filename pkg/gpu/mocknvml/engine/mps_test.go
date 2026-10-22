// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func mpsConfig() *DeviceConfig {
	return &DeviceConfig{Processes: []ProcessConfig{
		{PID: 100, Type: "C", Name: "nvidia-cuda-mps-server", UsedMemoryMiB: 32},
		{PID: 200, Type: "G", Name: "Xorg", UsedMemoryMiB: 128},
		{PID: 300, Type: "M", Name: "train.py", UsedMemoryMiB: 4096},
	}}
}

// TestGetMPSComputeRunningProcesses_ListsOnlyMPSClients covers the split NVML
// draws under MPS: a client's work runs in the MPS server's context, so the
// compute list shows the server and the MPS list shows the client, and neither
// shows the other.
func TestGetMPSComputeRunningProcesses_ListsOnlyMPSClients(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, mpsConfig())

	mps, ret := dev.GetMPSComputeRunningProcesses()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, mps, 1)
	require.Equal(t, uint32(300), mps[0].Pid)
	require.Equal(t, uint64(4096*oneMiB), mps[0].UsedGpuMemory)

	compute, ret := dev.GetComputeRunningProcesses()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, compute, 1)
	require.Equal(t, uint32(100), compute[0].Pid, "an MPS client is not a compute process of its own")

	graphics, ret := dev.GetGraphicsRunningProcesses()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, graphics, 1)
	require.Equal(t, uint32(200), graphics[0].Pid)
}

func TestRunningProcessesByMode_SelectsTheListTheModeNames(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, mpsConfig())

	for mode, want := range map[nvml.ProcessMode][]uint32{
		nvml.PROCESS_MODE_COMPUTE:  {100},
		nvml.PROCESS_MODE_GRAPHICS: {200},
		nvml.PROCESS_MODE_MPS:      {300},
		nvml.PROCESS_MODE_ALL:      {100, 200, 300},
	} {
		procs, ret := dev.RunningProcessesByMode(mode)
		require.Equal(t, nvml.SUCCESS, ret, "mode %d", mode)
		pids := make([]uint32, 0, len(procs))
		for _, p := range procs {
			pids = append(pids, p.Pid)
		}
		require.Equal(t, want, pids, "mode %d", mode)
	}

	_, ret := dev.RunningProcessesByMode(nvml.PROCESS_MODE_MAX)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

// TestRunningProcesses_ReportNoMIGInstance covers the instance IDs a consumer
// reads to attribute a process to a MIG instance: 0 is a valid instance ID, so a
// process outside MIG must carry NVML's "none" value instead.
func TestRunningProcesses_ReportNoMIGInstance(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, mpsConfig())

	procs, ret := dev.RunningProcessesByMode(nvml.PROCESS_MODE_ALL)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, procs, 3)
	for _, p := range procs {
		require.Equal(t, uint32(0xFFFFFFFF), p.GpuInstanceId, "pid %d", p.Pid)
		require.Equal(t, uint32(0xFFFFFFFF), p.ComputeInstanceId, "pid %d", p.Pid)
	}
}

func TestGetMPSComputeRunningProcesses_EmptyWithoutMPSClients(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Processes: []ProcessConfig{{PID: 100, Type: "C"}}})

	mps, ret := dev.GetMPSComputeRunningProcesses()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, mps)
}
