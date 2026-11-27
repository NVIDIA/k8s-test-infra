// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

var migProfileQueries = []struct {
	name    string
	version int
	profile int
	byID    bool
}{
	{"unversioned", 1, 0, false},
	{"v2", 2, 0, false},
	{"v3", 3, 0, false},
	{"byID-v2", 2, 19, true},
	{"byID-v3", 3, 19, true},
}

// The singleton engine and environment fixture prevent parallel subtests.
func TestMIGMetadataExports(t *testing.T) {
	b := newBridgeEngine(t, strings.ReplaceAll(migOffConfig, "550.163.01", "580.0.0"))
	h := b.handle(t, 0)
	for _, mode := range []int{nvml.DEVICE_MIG_DISABLE, nvml.DEVICE_MIG_ENABLE} {
		t.Run(strconv.Itoa(mode), func(t *testing.T) {
			ret, activation := migSetMode(h, mode)
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, nvml.SUCCESS, activation)
			for _, query := range migProfileQueries {
				t.Run(query.name, func(t *testing.T) {
					info, name, ret := migProfileInfoForTest(h, query.profile, query.version, query.byID)
					require.Equal(t, nvml.SUCCESS, ret)
					require.EqualValues(t, 19, info.Id)
					require.EqualValues(t, 7, info.InstanceCount)
					require.EqualValues(t, 4864, info.MemorySizeMB)
					if query.version > 1 {
						require.Equal(t, "MIG 1g.5gb", name)
					}
					_, _, ret = migProfileInfoForTest(h, 999, query.version, query.byID)
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
					_, _, ret = migProfileInfoForTest(nil, 999, query.version, query.byID)
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
				})
			}
			for version := range 3 {
				t.Run(fmt.Sprintf("placements-v%d", version), func(t *testing.T) {
					placements, ret := migPlacementsForTest(h, 19, version)
					require.Equal(t, nvml.SUCCESS, ret)
					require.Len(t, placements, 7)
					require.Equal(t, nvml.GpuInstancePlacement{Start: 6, Size: 1}, placements[6])
					_, ret = migPlacementsForTest(h, 999, version)
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
				})
			}
			capacity, ret := migCapacityForTest(h, 19)
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, 7, capacity)
			_, ret = migCapacityForTest(h, 999)
			require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
			if mode == nvml.DEVICE_MIG_DISABLE {
				for version := 1; version <= 3; version++ {
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, migComputeProfileForTest(nil, version))
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, migComputeProfileForTest(h, version))
				}
				require.Equal(t, nvml.ERROR_NOT_SUPPORTED, migCreateForTest(h, 19, nil))
				require.Equal(t, nvml.ERROR_NOT_SUPPORTED, migCreateForTest(h, 19, &nvml.GpuInstancePlacement{Size: 1}))
			} else {
				require.Equal(t, nvml.SUCCESS, migCreateForTest(h, 19, nil))
				require.Equal(t, nvml.SUCCESS, migCreateForTest(h, 19, &nvml.GpuInstancePlacement{Start: 1, Size: 1}))
				remaining, ret := migCapacityForTest(h, 19)
				require.Equal(t, nvml.SUCCESS, ret)
				require.Equal(t, 5, remaining)
			}
		})
	}
}

func TestMIGMetadataExports_LostDevice(t *testing.T) {
	b := newBridgeEngine(t, strings.ReplaceAll(migOffConfig, "550.163.01", "580.0.0"))
	h := b.handle(t, 0)
	b.fail(t, "lost")
	_, ret := b.LookupConfigurableDevice(h).GetTemperature(nvml.TEMPERATURE_GPU)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
	for _, handle := range []unsafe.Pointer{h, nil} {
		want := nvml.ERROR_GPU_IS_LOST
		if handle == nil {
			want = nvml.ERROR_INVALID_ARGUMENT
		}
		for _, query := range migProfileQueries {
			_, _, ret := migProfileInfoForTest(handle, 999, query.version, query.byID)
			require.Equal(t, want, ret, query.name)
		}
		for version := range 3 {
			_, ret := migPlacementsForTest(handle, 999, version)
			require.Equal(t, want, ret)
		}
		_, ret := migCapacityForTest(handle, 999)
		require.Equal(t, want, ret)
	}
}
