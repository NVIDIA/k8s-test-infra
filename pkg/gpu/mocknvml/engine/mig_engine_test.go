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
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// newFullyPartitionedEngine returns an initialised engine whose every device is
// an A100 declaring the maximum seven single-slice partitions.
func newFullyPartitionedEngine(t *testing.T, devices int) *Engine {
	t.Helper()

	e := NewEngine(&Config{
		NumDevices:    devices,
		DriverVersion: "550.54.15",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			DeviceDefaults: DeviceConfig{
				Name:   "NVIDIA A100-SXM4-40GB",
				Memory: &MemoryConfig{TotalBytes: a100_40GiB},
				MIG: &MIGConfig{
					ModeCurrent:     "enabled",
					ModePending:     "enabled",
					MaxGPUInstances: 7,
					GPUInstances:    []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
				},
			},
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { e.Shutdown() }) //nolint:errcheck // teardown

	return e
}

// TestMigDeviceHandles_FullyPartitionedNode is a regression test for the device
// handle table being sized to MaxDevices alone. MIG devices share that table
// with physical GPUs, so on a full node the GPUs used up every slot and each
// MIG device got a nil handle — returned as SUCCESS, which then failed every
// lookup the caller made through it.
func TestMigDeviceHandles_FullyPartitionedNode(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, MaxDevices)

	seen := make(map[unsafe.Pointer]struct{})
	for index := range MaxDevices {
		deviceHandle, ret := e.DeviceGetHandleByIndex(index)
		require.Equal(t, nvml.SUCCESS, ret)
		require.NotNil(t, deviceHandle)

		for migIndex := range 7 {
			migHandle, ret := e.DeviceGetMigDeviceHandleByIndex(deviceHandle, migIndex)
			require.Equal(t, nvml.SUCCESS, ret, "device %d MIG device %d", index, migIndex)
			require.NotNil(t, migHandle, "device %d MIG device %d", index, migIndex)

			require.NotContains(t, seen, migHandle, "handles must be distinct across the node")
			seen[migHandle] = struct{}{}

			// A handle the caller cannot resolve is worse than an error: it
			// looks like a device until the first query fails.
			migDev := e.LookupConfigurableDevice(migHandle)
			require.NotNil(t, migDev)
			isMig, ret := migDev.IsMigDeviceHandle()
			require.Equal(t, nvml.SUCCESS, ret)
			require.True(t, isMig)
		}
	}
	require.Len(t, seen, MaxDevices*7)
}

// TestGpuInstanceHandleLifecycle covers the handle-facing half of the
// lifecycle: instance handles are their own opaque pointers, and destroying an
// instance must invalidate both its handle and the MIG device derived from it.
func TestGpuInstanceHandleLifecycle(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, 1)

	deviceHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	instances, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, instances, 7, "the declared layout is visible through the handle API")

	giHandle := instances[0]
	info, parentHandle, ret := e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, deviceHandle, parentHandle,
		"the info's parent must be the handle the caller already holds, not a fresh registration")
	require.Equal(t, uint32(nvml.GPU_INSTANCE_PROFILE_1_SLICE), info.ProfileId)

	migHandle, ret := e.DeviceGetMigDeviceHandleByIndex(deviceHandle, 0)
	require.Equal(t, nvml.SUCCESS, ret)

	require.Equal(t, nvml.SUCCESS, e.GpuInstanceDestroy(giHandle))

	_, _, ret = e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "a destroyed instance's handle must stop resolving")
	require.Nil(t, e.LookupConfigurableDevice(migHandle),
		"the MIG device derived from a destroyed instance must stop resolving too")

	remaining, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, remaining, 6)
}

// TestComputeInstanceHandleLifecycle checks the same for compute instances,
// including that the info's embedded GPU instance handle is the one the caller
// holds rather than a duplicate registration.
func TestComputeInstanceHandleLifecycle(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, 1)

	deviceHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	instances, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	giHandle := instances[0]

	computeInstances, ret := e.GpuInstanceGetComputeInstances(giHandle, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, computeInstances, 1, "a declared partition gets one spanning compute instance")

	ciHandle := computeInstances[0]
	_, parentHandle, embeddedGI, ret := e.ComputeInstanceGetInfo(ciHandle)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, deviceHandle, parentHandle)
	require.Equal(t, giHandle, embeddedGI)

	require.Equal(t, nvml.SUCCESS, e.ComputeInstanceDestroy(ciHandle))
	_, _, _, ret = e.ComputeInstanceGetInfo(ciHandle)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)

	// The GPU instance outlives its compute instance, so it can be refilled.
	_, ret = e.GpuInstanceCreateComputeInstance(giHandle, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, nil)
	require.Equal(t, nvml.SUCCESS, ret)
}

// TestMigProfileNames_ThroughHandles pins the names the versioned profile-info
// structs carry. They are what nvidia-smi prints for a partition.
func TestMigProfileNames_ThroughHandles(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, 1)

	deviceHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	name, ret := e.DeviceGetGpuInstanceProfileName(deviceHandle, nvml.GPU_INSTANCE_PROFILE_3_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "MIG 3g.20gb", name)

	instances, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)

	ciName, ret := e.GpuInstanceGetComputeInstanceProfileName(instances[0], nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "MIG 1g.5gb", ciName)
}
