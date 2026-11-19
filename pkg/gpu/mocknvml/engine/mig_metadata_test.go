// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"strconv"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func newMIGMetadataEngine(t *testing.T, cfg *DeviceConfig) (*Engine, unsafe.Pointer) {
	t.Helper()
	e := NewEngine(&Config{NumDevices: 1, YAMLConfig: &YAMLConfig{Version: "1.0", DeviceDefaults: *cfg}})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	h, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	return e, h
}

// Exercise the real server construction, including its initially empty instance
// map, and every row of each shipped board's table (including +me/rev variants).
func TestMIGMetadata_AllShippedProfiles(t *testing.T) {
	t.Parallel()
	for _, board := range migGoldenBoards {
		t.Run(board.profile, func(t *testing.T) {
			t.Parallel()
			cfg := &DeviceConfig{Name: board.deviceName, Memory: &MemoryConfig{TotalBytes: board.memoryBytes}, MIG: migConfigOfShippedProfile(t, board.profile)}
			cfg.MIG.ModeCurrent, cfg.MIG.ModePending = "disabled", "disabled"
			cfg.MIG.GPUInstances = nil
			e, h := newMIGMetadataEngine(t, cfg)
			dev := e.LookupConfigurableDevice(h)
			for _, spec := range cfg.MIG.SupportedProfiles {
				t.Run(spec.Name, func(t *testing.T) {
					profile, ok := gpuInstanceProfileEnum(spec.NVMLProfile)
					require.True(t, ok)
					info, ret := e.DeviceGetGpuInstanceProfileInfo(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, spec.MemoryMB, info.MemorySizeMB)
					require.EqualValues(t, spec.Instances, info.InstanceCount)
					name, ret := e.DeviceGetGpuInstanceProfileName(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, "MIG "+spec.Name, name)
					reported, ret := e.DeviceGpuInstanceReportedProfileID(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, spec.ProfileID, reported)
					resolved, ret := e.DeviceGpuInstanceProfileEnum(h, reported)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, profile, resolved)
					placements, ret := e.DeviceGetGpuInstancePossiblePlacements(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Len(t, placements, len(spec.Placements))
					for i, p := range placements {
						require.Equal(t, nvml.GpuInstancePlacement{Start: spec.Placements[i].Start, Size: spec.Placements[i].Size}, p)
					}
					capacity, ret := e.DeviceGetGpuInstanceRemainingCapacity(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, spec.Instances, capacity)
					for _, placement := range []*nvml.GpuInstancePlacement{nil, &placements[0]} {
						gi, ret := e.DeviceCreateGpuInstance(h, profile, placement)
						require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
						require.Nil(t, gi)
					}
					_, ret = e.DeviceGetGpuInstances(h, profile)
					require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
					enableMIG(t, dev)
					enabled, ret := e.DeviceGetGpuInstanceProfileInfo(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, info, enabled)
					gi, ret := e.DeviceCreateGpuInstance(h, profile, &placements[0])
					require.Equal(t, nvml.SUCCESS, ret)
					remaining, ret := e.DeviceGetGpuInstanceRemainingCapacity(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, capacity-1, remaining)
					_, ret = e.GpuInstanceGetComputeInstanceProfileInfo(gi, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
					require.Equal(t, nvml.SUCCESS, ret)
					activation, ret := dev.SetMigMode(nvml.DEVICE_MIG_DISABLE)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, nvml.SUCCESS, activation)
					_, ret = e.GpuInstanceGetComputeInstanceProfileInfo(gi, 0, 0)
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "disabling MIG retires instance handles")
					_, ret = e.GpuInstanceCreateComputeInstance(gi, 0, nil)
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
					restored, ret := e.DeviceGetGpuInstanceRemainingCapacity(h, profile)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, capacity, restored)
				})
			}
			current, pending, ret := dev.GetMigMode()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, nvml.DEVICE_MIG_DISABLE, current)
			require.Equal(t, current, pending)
			require.Empty(t, dev.migState.liveGpuInstances(dev))
			_, ret = e.DeviceGetGpuInstanceById(h, 0)
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
			_, ret = e.DeviceGetMigDeviceHandleByIndex(h, 0)
			require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
		})
	}
}

func TestMIGMetadata_Errors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		mig  *MIGConfig
		want nvml.Return
	}{
		{"missing", nil, nvml.ERROR_NOT_SUPPORTED},
		{"unconfigured", &MIGConfig{ModeCurrent: "disabled", MaxGPUInstances: 7}, nvml.ERROR_NOT_SUPPORTED},
		{"disabled", a100MIGConfig().MIG, nvml.SUCCESS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, h := newMIGMetadataEngine(t, &DeviceConfig{MIG: tc.mig})
			queries := map[string]func(unsafe.Pointer, int) nvml.Return{
				"info": func(h unsafe.Pointer, p int) nvml.Return { _, r := e.DeviceGetGpuInstanceProfileInfo(h, p); return r },
				"name": func(h unsafe.Pointer, p int) nvml.Return { _, r := e.DeviceGetGpuInstanceProfileName(h, p); return r },
				"placements": func(h unsafe.Pointer, p int) nvml.Return {
					_, r := e.DeviceGetGpuInstancePossiblePlacements(h, p)
					return r
				},
				"capacity": func(h unsafe.Pointer, p int) nvml.Return {
					_, r := e.DeviceGetGpuInstanceRemainingCapacity(h, p)
					return r
				},
			}
			for name, query := range queries {
				t.Run(name, func(t *testing.T) {
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, query(nil, -1))
					require.Equal(t, tc.want, query(h, 0))
					for _, p := range []int{-1, nvml.GPU_INSTANCE_PROFILE_COUNT} {
						want := tc.want
						if want == nvml.SUCCESS {
							want = nvml.ERROR_INVALID_ARGUMENT
						}
						require.Equal(t, want, query(h, p))
					}
					require.Equal(t, nvml.ERROR_NOT_SUPPORTED, query(h, nvml.GPU_INSTANCE_PROFILE_8_SLICE))
				})
			}
			_, ret := e.DeviceGpuInstanceProfileEnum(h, 999)
			want := tc.want
			if want == nvml.SUCCESS {
				want = nvml.ERROR_INVALID_ARGUMENT
			}
			require.Equal(t, want, ret)
			dev := e.LookupConfigurableDevice(h)
			dev.failure.Store(newFailureInjector(&FailureInjectionConfig{Mode: FailureModeLost}))
			_, ret = dev.GetTemperature(nvml.TEMPERATURE_GPU)
			require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
			for name, query := range queries {
				require.Equal(t, nvml.ERROR_GPU_IS_LOST, query(h, -1), name)
			}
			_, ret = e.DeviceGpuInstanceProfileEnum(h, 999)
			require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
			_, ret = e.DeviceGpuInstanceReportedProfileID(h, 0)
			require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
		})
	}
}

func TestMIGMetadata_ConcurrentModeChanges(t *testing.T) {
	t.Parallel()
	e, h := newMIGMetadataEngine(t, a100MIGConfig())
	dev := e.LookupConfigurableDevice(h)
	t.Run("concurrent", func(t *testing.T) {
		for worker := range 4 {
			t.Run(strconv.Itoa(worker), func(t *testing.T) {
				t.Parallel()
				for i := range 50 {
					if worker == 0 {
						activation, ret := dev.SetMigMode(i % 2)
						require.Equal(t, nvml.SUCCESS, ret)
						require.Equal(t, nvml.SUCCESS, activation)
						continue
					}
					_, ret := e.DeviceGetGpuInstanceProfileInfo(h, 0)
					require.Equal(t, nvml.SUCCESS, ret)
					_, ret = e.DeviceGpuInstanceProfileEnum(h, 19)
					require.Equal(t, nvml.SUCCESS, ret)
					placements, ret := e.DeviceGetGpuInstancePossiblePlacements(h, 0)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Len(t, placements, 7)
					capacity, ret := e.DeviceGetGpuInstanceRemainingCapacity(h, 0)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Equal(t, 7, capacity)
				}
			})
		}
	})
}

func TestMIGMetadata_RejectsMIGDevice(t *testing.T) {
	t.Parallel()
	e := newFullyPartitionedEngine(t, 1)
	h, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	mig, ret := e.DeviceGetMigDeviceHandleByIndex(h, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	_, ret = e.DeviceGetGpuInstanceProfileInfo(mig, -1)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = e.DeviceGpuInstanceProfileEnum(mig, 999)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = e.DeviceGpuInstanceReportedProfileID(mig, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = e.DeviceGetGpuInstancePossiblePlacements(mig, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = e.DeviceGetGpuInstanceRemainingCapacity(mig, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = e.DeviceCreateGpuInstance(mig, 0, nil)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}
