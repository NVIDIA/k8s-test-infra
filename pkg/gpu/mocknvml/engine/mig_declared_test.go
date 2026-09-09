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

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func migYAMLConfig(mig *MIGConfig) *YAMLConfig {
	return &YAMLConfig{
		Version: "1.0",
		DeviceDefaults: DeviceConfig{
			Name:   "NVIDIA A100-SXM4-40GB",
			Memory: &MemoryConfig{TotalBytes: a100_40GiB},
			MIG:    mig,
		},
	}
}

func TestDeclaredMIGLayout_UniformPartitions(t *testing.T) {
	t.Parallel()

	layout := DeclaredMIGLayout(&Config{
		NumDevices: 2,
		YAMLConfig: migYAMLConfig(&MIGConfig{
			ModeCurrent:  "enabled",
			ModePending:  "enabled",
			GPUInstances: []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
		}),
	})

	require.Len(t, layout, 2)
	for _, gpu := range layout {
		require.Len(t, gpu.GPUInstances, 7)
		for _, gi := range gpu.GPUInstances {
			require.Equal(t, "1g.5gb", gi.Profile)
			require.Equal(t, []uint32{0}, gi.ComputeInstanceIDs,
				"a partition with no declared compute slices gets one spanning instance")
		}
	}
	require.Equal(t, 0, layout[0].GPUIndex)
	require.Equal(t, 1, layout[1].GPUIndex)
}

// TestDeclaredMIGLayout_MatchesNVMLEnumeration is the guarantee the caps
// surface rests on: the IDs the layout reports must be the IDs a consumer sees
// through NVML. If they ever diverge, the device plugin maps a MIG device to
// another partition's cap minor.
func TestDeclaredMIGLayout_MatchesNVMLEnumeration(t *testing.T) {
	t.Parallel()

	config := &Config{
		NumDevices: 1,
		YAMLConfig: migYAMLConfig(&MIGConfig{
			ModeCurrent: "enabled",
			ModePending: "enabled",
			GPUInstances: []MIGGPUInstanceConfig{
				{Profile: "3g.20gb", Count: 1},
				{Profile: "2g.10gb", Count: 1},
				{Profile: "1g.5gb", Count: 2},
			},
		}),
	}

	type partition struct {
		gi, ci uint32
	}
	var fromLayout []partition
	for _, gi := range DeclaredMIGLayout(config)[0].GPUInstances {
		for _, ci := range gi.ComputeInstanceIDs {
			fromLayout = append(fromLayout, partition{gi.ID, ci})
		}
	}

	e := NewEngine(config)
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { e.Shutdown() }) //nolint:errcheck // teardown
	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	dev := e.LookupConfigurableDevice(handle)
	require.NotNil(t, dev)

	var fromNVML []partition
	for index := 0; ; index++ {
		migDev, ret := dev.GetMigDeviceHandleByIndex(index)
		if ret == nvml.ERROR_NOT_FOUND {
			break
		}
		require.Equal(t, nvml.SUCCESS, ret)

		gi, ret := migDev.GetGpuInstanceId()
		require.Equal(t, nvml.SUCCESS, ret)
		ci, ret := migDev.GetComputeInstanceId()
		require.Equal(t, nvml.SUCCESS, ret)
		fromNVML = append(fromNVML, partition{uint32(gi), uint32(ci)}) //nolint:gosec // IDs are small
	}

	require.NotEmpty(t, fromNVML)
	require.Equal(t, fromNVML, fromLayout, "layout order and IDs must match NVML enumeration exactly")
}

func TestDeclaredMIGLayout_MIGDisabled(t *testing.T) {
	t.Parallel()

	// A profile can carry the layout its board would be partitioned into and
	// still boot with MIG off. Nothing is partitioned, so nothing is staged.
	layout := DeclaredMIGLayout(&Config{
		NumDevices: 2,
		YAMLConfig: migYAMLConfig(&MIGConfig{
			ModeCurrent:  "disabled",
			GPUInstances: []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
		}),
	})
	require.Empty(t, layout)
}

func TestDeclaredMIGLayout_NoMIGConfig(t *testing.T) {
	t.Parallel()

	require.Empty(t, DeclaredMIGLayout(&Config{NumDevices: 4, YAMLConfig: migYAMLConfig(nil)}))
	require.Empty(t, DeclaredMIGLayout(nil))
}

// TestDeclaredMIGLayout_PerDeviceOverride covers a mixed node: only the devices
// a profile actually partitions appear.
func TestDeclaredMIGLayout_PerDeviceOverride(t *testing.T) {
	t.Parallel()

	cfg := migYAMLConfig(nil)
	cfg.Devices = []DeviceOverride{{
		Index: 1,
		DeviceConfig: DeviceConfig{
			MIG: &MIGConfig{
				ModeCurrent:  "enabled",
				ModePending:  "enabled",
				GPUInstances: []MIGGPUInstanceConfig{{Profile: "2g.10gb", Count: 3}},
			},
		},
	}}

	layout := DeclaredMIGLayout(&Config{NumDevices: 4, YAMLConfig: cfg})

	require.Len(t, layout, 1, "only the overridden device is partitioned")
	require.Equal(t, 1, layout[0].GPUIndex)
	require.Len(t, layout[0].GPUInstances, 3)
	require.Equal(t, "2g.10gb", layout[0].GPUInstances[0].Profile)
}

// A modern driver resolves a MIG device's own UUID to its handle, and consumers
// rely on it: the device plugin's health monitor looks a partition up by UUID
// and only falls back to parsing the legacy MIG-GPU-<parent>/<gi>/<ci> spelling
// if that fails. Without this the plugin enumerated every partition, failed to
// place any of them, and marked all of them unhealthy — advertising zero
// allocatable GPUs while looking healthy itself.
func TestDeviceGetHandleByUUID_ResolvesMigDevices(t *testing.T) {
	t.Parallel()

	e := NewEngine(&Config{
		NumDevices: 2,
		YAMLConfig: migYAMLConfig(&MIGConfig{
			ModeCurrent:  "enabled",
			ModePending:  "enabled",
			GPUInstances: []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
		}),
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	defer func() { _ = e.Shutdown() }()

	seen := map[string]bool{}
	for index := range 2 {
		parent, ret := e.DeviceGetHandleByIndex(index)
		require.Equal(t, nvml.SUCCESS, ret)

		for position := range 7 {
			migHandle, ret := e.DeviceGetMigDeviceHandleByIndex(parent, position)
			require.Equal(t, nvml.SUCCESS, ret, "gpu %d mig %d", index, position)

			uuid, ret := e.LookupConfigurableDevice(migHandle).GetUUID()
			require.Equal(t, nvml.SUCCESS, ret)
			require.False(t, seen[uuid], "MIG UUID %s reported twice", uuid)
			seen[uuid] = true

			byUUID, ret := e.DeviceGetHandleByUUID(uuid)
			require.Equal(t, nvml.SUCCESS, ret, "lookup of MIG UUID %s", uuid)
			require.Equal(t, migHandle, byUUID,
				"a MIG UUID must resolve to the same handle the index walk returned")

			// The placement the health monitor reads off that handle has to be
			// the partition's own, not its parent's.
			isMig, ret := e.LookupConfigurableDevice(byUUID).IsMigDeviceHandle()
			require.Equal(t, nvml.SUCCESS, ret)
			require.True(t, isMig, "%s resolved to a handle that denies being a MIG device", uuid)
		}
	}
	require.Len(t, seen, 14)
}
