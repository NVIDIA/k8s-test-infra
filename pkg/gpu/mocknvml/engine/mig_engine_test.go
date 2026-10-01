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
					ModeCurrent:       "enabled",
					ModePending:       "enabled",
					MaxGPUInstances:   7,
					SupportedProfiles: a100SupportedProfiles(),
					GPUInstances:      []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
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

	// The listing is instance-ID ordered, which is what lets this test and the
	// ones below name a partition by position. It is not incidental: the
	// reconciler tears instances down in this order so a repartition is
	// deterministic, so it is pinned here rather than assumed.
	ids := make([]uint32, 0, len(instances))
	for _, handle := range instances {
		info, _, ret := e.GpuInstanceGetInfo(handle)
		require.Equal(t, nvml.SUCCESS, ret)
		ids = append(ids, info.Id)
	}
	require.IsIncreasing(t, ids, "GPU instances must enumerate in instance-ID order")

	giHandle := instances[0]
	info, parentHandle, ret := e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, deviceHandle, parentHandle,
		"the info's parent must be the handle the caller already holds, not a fresh registration")
	require.Equal(t, uint32(nvml.GPU_INSTANCE_PROFILE_1_SLICE), info.ProfileId)

	migHandle, ret := e.DeviceGetMigDeviceHandleByIndex(deviceHandle, 0)
	require.Equal(t, nvml.SUCCESS, ret)

	// The declared partition holds a compute instance, which NVML requires be
	// gone first; the refusal itself is covered by
	// TestGpuInstanceDestroy_RefusedWhileAComputeInstanceLives.
	computeInstances, ret := e.GpuInstanceGetComputeInstances(giHandle, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, computeInstances, 1)
	require.Equal(t, nvml.SUCCESS, e.ComputeInstanceDestroy(computeInstances[0]))

	require.Equal(t, nvml.SUCCESS, e.GpuInstanceDestroy(giHandle))

	_, _, ret = e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "a destroyed instance's handle must stop resolving")
	require.Nil(t, e.LookupConfigurableDevice(migHandle),
		"the MIG device derived from a destroyed instance must stop resolving too")

	remaining, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, remaining, 6)
}

// NVML refuses to tear down a GPU instance that still holds a compute
// instance, which is why every partitioning tool destroys compute instances
// first. A mock that cascades instead lets a tool with that order wrong pass
// here and fail on hardware — the one kind of failure a mock must not hide.
func TestGpuInstanceDestroy_RefusedWhileAComputeInstanceLives(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, 1)

	deviceHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	instances, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, instances, 7)
	giHandle := instances[0]

	computeInstances, ret := e.GpuInstanceGetComputeInstances(giHandle, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, computeInstances, 1)
	ciHandle := computeInstances[0]

	require.Equal(t, nvml.ERROR_IN_USE, e.GpuInstanceDestroy(giHandle),
		"a GPU instance still holding a compute instance must not be destroyable")

	// A refusal has to leave the partition whole. Tearing part of it down and
	// then reporting failure would be worse than either outcome on its own,
	// because a caller that retries correctly would find the state already
	// half gone.
	_, _, ret = e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.SUCCESS, ret, "a refused destroy must leave the GPU instance alive")
	_, _, _, ret = e.ComputeInstanceGetInfo(ciHandle)
	require.Equal(t, nvml.SUCCESS, ret, "a refused destroy must leave the compute instance alive")
	require.NotNil(t, e.LookupConfigurableDevice(mustMigDeviceHandle(t, e, deviceHandle, 0)),
		"a refused destroy must leave the derived MIG device alive")

	// And the order a partitioning tool is written to still works.
	require.Equal(t, nvml.SUCCESS, e.ComputeInstanceDestroy(ciHandle))
	require.Equal(t, nvml.SUCCESS, e.GpuInstanceDestroy(giHandle))

	survivors, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, survivors, 6)
}

// A GPU instance with no compute instance is destroyable, which is the control
// that keeps the refusal above about occupancy rather than about GPU instances
// in general.
func TestGpuInstanceDestroy_AllowedWhenBare(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, 1)

	deviceHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	instances, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	giHandle := instances[0]

	computeInstances, ret := e.GpuInstanceGetComputeInstances(giHandle, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, computeInstances, 1)
	require.Equal(t, nvml.SUCCESS, e.ComputeInstanceDestroy(computeInstances[0]))

	require.Equal(t, nvml.SUCCESS, e.GpuInstanceDestroy(giHandle),
		"a GPU instance holding nothing must be destroyable")
}

func mustMigDeviceHandle(t *testing.T, e *Engine, parent unsafe.Pointer, index int) unsafe.Pointer {
	t.Helper()
	handle, ret := e.DeviceGetMigDeviceHandleByIndex(parent, index)
	require.Equal(t, nvml.SUCCESS, ret)
	return handle
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

// TestSetMigMode_RetiresInstanceHandles covers the two registries a wholesale
// teardown has to reach beyond the device table. Turning MIG off destroys the
// GPU and compute instances as well as the MIG devices derived from them, so a
// caller still holding a handle to any of the three must stop getting answers,
// the way NVML fails those calls once the partitioning is gone.
//
// GpuInstanceDestroy already retires all three. This is the same guarantee for
// the paths that tear the board down without naming an instance.
func TestSetMigMode_RetiresInstanceHandles(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, 1)

	deviceHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	instances, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, instances, 7)
	giHandle := instances[0]

	computeInstances, ret := e.GpuInstanceGetComputeInstances(giHandle, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, computeInstances, 1)
	ciHandle := computeInstances[0]

	migHandle, ret := e.DeviceGetMigDeviceHandleByIndex(deviceHandle, 0)
	require.Equal(t, nvml.SUCCESS, ret)

	dev := e.LookupConfigurableDevice(deviceHandle)
	require.NotNil(t, dev)
	ret, _ = dev.SetMigMode(nvml.DEVICE_MIG_DISABLE)
	require.Equal(t, nvml.SUCCESS, ret)

	require.Nil(t, e.LookupConfigurableDevice(migHandle),
		"the MIG device derived from a destroyed partition must stop resolving")
	_, _, ret = e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret,
		"a GPU instance handle must stop resolving once MIG is off")
	_, _, _, ret = e.ComputeInstanceGetInfo(ciHandle)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret,
		"a compute instance handle must stop resolving once MIG is off")
}

// TestSetMigMode_RetiresABareInstanceHandle covers the instance a teardown
// cannot find by walking MIG devices. MIG devices are derived from compute
// instances, so a GPU instance holding none — what `nvidia-smi mig -cgi`
// without -C leaves behind — has no device pointing back at it, and its handle
// has to be retired from the instance tree rather than from the device table.
func TestSetMigMode_RetiresABareInstanceHandle(t *testing.T) {
	t.Parallel()

	e := newFullyPartitionedEngine(t, 1)

	deviceHandle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	instances, ret := e.DeviceGetGpuInstances(deviceHandle, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, instances, 7)
	giHandle := instances[0]

	// Emptying the instance is what makes it bare: the GPU instance outlives
	// its compute instance, and the MIG device goes with the compute instance.
	computeInstances, ret := e.GpuInstanceGetComputeInstances(giHandle, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, computeInstances, 1)
	require.Equal(t, nvml.SUCCESS, e.ComputeInstanceDestroy(computeInstances[0]))

	_, _, ret = e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.SUCCESS, ret, "the emptied GPU instance is still live")

	dev := e.LookupConfigurableDevice(deviceHandle)
	require.NotNil(t, dev)
	ret, _ = dev.SetMigMode(nvml.DEVICE_MIG_DISABLE)
	require.Equal(t, nvml.SUCCESS, ret)

	_, _, ret = e.GpuInstanceGetInfo(giHandle)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret,
		"a GPU instance with no compute instance must stop resolving once MIG is off")
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
