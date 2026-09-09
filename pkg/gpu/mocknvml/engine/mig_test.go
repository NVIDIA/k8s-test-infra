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

// a100MIGConfig is a MIG-capable A100 with MIG left off, the state a real
// board ships in.
func a100MIGConfig() *DeviceConfig {
	return &DeviceConfig{
		Name:   "NVIDIA A100-SXM4-40GB",
		Memory: &MemoryConfig{TotalBytes: a100_40GiB, FreeBytes: a100_40GiB},
		MIG: &MIGConfig{
			ModeCurrent:     "disabled",
			ModePending:     "disabled",
			MaxGPUInstances: 7,
		},
	}
}

// enableMIG turns MIG on the way a consumer would, through NVML.
func enableMIG(t *testing.T, dev *ConfigurableDevice) {
	t.Helper()
	activation, ret := dev.SetMigMode(nvml.DEVICE_MIG_ENABLE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.SUCCESS, activation)
}

// createOneSliceGI creates a single 1-slice GPU instance and returns it.
func createOneSliceGI(t *testing.T, dev *ConfigurableDevice) nvml.GpuInstance {
	t.Helper()
	info, ret := dev.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	gi, ret := dev.CreateGpuInstance(&info)
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotNil(t, gi)
	return gi
}

// createSpanningCI creates a compute instance spanning the whole GPU instance.
func createSpanningCI(t *testing.T, gi nvml.GpuInstance) nvml.ComputeInstance {
	t.Helper()
	ciInfo, ret := gi.GetComputeInstanceProfileInfo(
		nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
	require.Equal(t, nvml.SUCCESS, ret)
	ci, ret := gi.CreateComputeInstance(&ciInfo)
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotNil(t, ci)
	return ci
}

// TestGetMigMode_UnsupportedBoard checks that a board with no MIG tables
// reports MIG as unsupported rather than as "supported but disabled".
// go-nvlib's IsMigCapable keys off exactly this distinction, so answering
// SUCCESS here would make a T4 look MIG-capable.
func TestGetMigMode_UnsupportedBoard(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, &DeviceConfig{Name: "Tesla T4"})

	_, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestGetMigMode_CapableButDisabled(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())

	current, pending, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.DEVICE_MIG_DISABLE, current)
	require.Equal(t, nvml.DEVICE_MIG_DISABLE, pending)
}

func TestSetMigMode(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	current, pending, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.DEVICE_MIG_ENABLE, current)
	require.Equal(t, nvml.DEVICE_MIG_ENABLE, pending)
}

func TestSetMigMode_UnsupportedBoard(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, &DeviceConfig{Name: "Tesla T4"})

	_, ret := dev.SetMigMode(nvml.DEVICE_MIG_ENABLE)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// TestCreateGpuInstance_RequiresMigEnabled pins the gating go-nvml's mock
// omits: on real hardware partitioning a GPU with MIG off is rejected.
func TestCreateGpuInstance_RequiresMigEnabled(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())

	_, ret := dev.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "profile info is unavailable while MIG is off")

	_, ret = dev.CreateGpuInstance(&nvml.GpuInstanceProfileInfo{Id: nvml.GPU_INSTANCE_PROFILE_1_SLICE})
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// TestGpuInstanceLifecycle walks the whole create/destroy cycle and checks
// that a MIG device only exists while both its GPU instance and its compute
// instance do.
func TestGpuInstanceLifecycle(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	count, ret := dev.GetMaxMigDeviceCount()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 7, count)

	_, ret = dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "enabling MIG alone creates no instances")

	gi := createOneSliceGI(t, dev)

	_, ret = dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret,
		"a GPU instance with no compute instance exposes no MIG device")

	ci := createSpanningCI(t, gi)

	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotNil(t, migDev)

	isMig, ret := migDev.IsMigDeviceHandle()
	require.Equal(t, nvml.SUCCESS, ret)
	require.True(t, isMig)

	require.Equal(t, nvml.SUCCESS, ci.Destroy())
	_, ret = dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret, "destroying the compute instance retires the MIG device")

	require.Equal(t, nvml.SUCCESS, gi.Destroy())
	gis, ret := dev.GetGpuInstances(&nvml.GpuInstanceProfileInfo{Id: nvml.GPU_INSTANCE_PROFILE_1_SLICE})
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, gis)
}

// TestCreateGpuInstance_ExhaustsPlacements checks that the mock runs out of
// room the way hardware does instead of accepting unbounded instances.
func TestCreateGpuInstance_ExhaustsPlacements(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	info, ret := dev.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)

	for i := range 7 {
		_, ret := dev.CreateGpuInstance(&info)
		require.Equal(t, nvml.SUCCESS, ret, "creating 1-slice instance %d", i)
	}

	_, ret = dev.CreateGpuInstance(&info)
	require.Equal(t, nvml.ERROR_INSUFFICIENT_RESOURCES, ret,
		"an A100 has seven slices, so the eighth 1-slice instance must not fit")
}

// TestCreateGpuInstance_RespectsOccupiedSlices checks that a wide instance is
// refused once narrower ones have taken the slices it would need.
func TestCreateGpuInstance_RespectsOccupiedSlices(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	oneSlice, ret := dev.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	fourSlice, ret := dev.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_4_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)

	// The only 4-slice placement on an A100 starts at slice 0, so a single
	// 1-slice instance there blocks it.
	_, ret = dev.CreateGpuInstance(&oneSlice)
	require.Equal(t, nvml.SUCCESS, ret)

	_, ret = dev.CreateGpuInstance(&fourSlice)
	require.Equal(t, nvml.ERROR_INSUFFICIENT_RESOURCES, ret)
}

// TestMigDeviceHandleByIndex_StableIdentity matters because the bridge's
// handle table keys on the device pointer: a fresh object per call would leak
// a new C handle on every enumeration and break handle equality for consumers
// that cache them.
func TestMigDeviceHandleByIndex_StableIdentity(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	createSpanningCI(t, createOneSliceGI(t, dev))

	first, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	second, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	require.Same(t, first, second)
}

func TestMigDevice_Attributes(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	createSpanningCI(t, createOneSliceGI(t, dev))

	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	attrs, ret := migDev.GetAttributes()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(1), attrs.GpuInstanceSliceCount)
	require.Equal(t, uint32(1), attrs.ComputeInstanceSliceCount)
	require.Equal(t, uint32(14), attrs.MultiprocessorCount)
	require.Equal(t, uint32(1), attrs.SharedCopyEngineCount)
	require.Equal(t, uint64(4864), attrs.MemorySizeMB,
		"go-nvlib derives the profile name from this, so it must be the slice's raw MiB")
}

// TestGetAttributes_ParentDevice pins that attributes are a MIG-only query,
// which is how a consumer tells a MIG handle from a full GPU.
func TestGetAttributes_ParentDevice(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())

	_, ret := dev.GetAttributes()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

func TestMigDevice_Identity(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	createSpanningCI(t, createOneSliceGI(t, dev))

	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	uuid, ret := migDev.GetUUID()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Regexp(t,
		`^MIG-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, uuid,
		"consumers parse the part after MIG- as a UUID")

	parentUUID, ret := dev.GetUUID()
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotEqual(t, parentUUID, uuid)

	name, ret := migDev.GetName()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "NVIDIA A100-SXM4-40GB MIG 1g.5gb", name)

	mem, ret := migDev.GetMemoryInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(4864)*oneMiB, mem.Total, "a MIG device reports its slice, not the board")

	// A MIG device is not itself MIG-capable.
	_, _, ret = migDev.GetMigMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// TestMigDeviceUUID_IsDeterministic matters because two processes load the
// mock independently and must agree on device identity, exactly as they
// already must for full-GPU UUIDs. The derivation is therefore a pure function
// of the parent UUID and the instance IDs.
func TestMigDeviceUUID_IsDeterministic(t *testing.T) {
	t.Parallel()

	const parent = "GPU-4d4f434b-0000-0000-0000-000000000000"

	// Pinned, because a change here renames every MIG device in every cluster
	// running the mock. The partition is legible in the fourth group.
	require.Equal(t, "MIG-4d4f434b-0000-0000-0100-000000000000", migDeviceUUID(parent, 1, 0))
	require.Equal(t, "MIG-4d4f434b-0000-0000-0201-000000000000", migDeviceUUID(parent, 2, 1))

	require.NotEqual(t, migDeviceUUID(parent, 1, 0), migDeviceUUID(parent, 2, 0),
		"a different GPU instance is a different MIG device")
	require.NotEqual(t, migDeviceUUID(parent, 1, 0), migDeviceUUID(parent, 1, 1),
		"a different compute instance is a different MIG device")
	require.NotEqual(t, migDeviceUUID(parent, 1, 0),
		migDeviceUUID("GPU-4d4f434b-0000-0000-0000-000000000001", 1, 0),
		"the same partition on a different GPU is a different MIG device")
}

// TestMigDeviceUUID_NonUUIDParent covers an operator-supplied devices[].uuid
// that is not UUID-shaped: the result must still parse as a UUID, because
// consumers parse the text after "MIG-" as one.
func TestMigDeviceUUID_NonUUIDParent(t *testing.T) {
	t.Parallel()

	got := migDeviceUUID("my-gpu", 1, 0)
	require.Regexp(t, `^MIG-[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`, got)
	require.Equal(t, got, migDeviceUUID("my-gpu", 1, 0), "still deterministic")
	require.NotEqual(t, got, migDeviceUUID("my-gpu", 1, 1))
	require.NotEqual(t, got, migDeviceUUID("other-gpu", 1, 0))
}

func TestMigDevice_ResolvesParent(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	createSpanningCI(t, createOneSliceGI(t, dev))

	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	parent, ret := migDev.GetDeviceHandleFromMigDeviceHandle()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, dev, parent)

	// The same query on a full GPU is an error: it has no parent.
	_, ret = dev.GetDeviceHandleFromMigDeviceHandle()
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
}

// TestMigDevice_InstanceIDs covers the two lookups go-nvlib uses to walk from
// a MIG device back to the instances that define its profile.
func TestMigDevice_InstanceIDs(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	gi := createOneSliceGI(t, dev)
	ci := createSpanningCI(t, gi)

	giInfo, ret := gi.GetInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	ciInfo, ret := ci.GetInfo()
	require.Equal(t, nvml.SUCCESS, ret)

	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	giID, ret := migDev.GetGpuInstanceId()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, int(giInfo.Id), giID)

	ciID, ret := migDev.GetComputeInstanceId()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, int(ciInfo.Id), ciID)

	// Both are MIG-only queries on a full GPU.
	_, ret = dev.GetGpuInstanceId()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, ret = dev.GetComputeInstanceId()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// TestInstanceLookupByID covers the by-ID lookups go-nvml's mock leaves
// unimplemented but go-nvlib's profile resolution depends on.
func TestInstanceLookupByID(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	gi := createOneSliceGI(t, dev)
	ci := createSpanningCI(t, gi)

	giInfo, ret := gi.GetInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	ciInfo, ret := ci.GetInfo()
	require.Equal(t, nvml.SUCCESS, ret)

	foundGI, ret := dev.GetGpuInstanceById(int(giInfo.Id))
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, gi, foundGI)

	foundCI, ret := gi.GetComputeInstanceById(int(ciInfo.Id))
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, ci, foundCI)

	_, ret = dev.GetGpuInstanceById(999)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
	_, ret = gi.GetComputeInstanceById(999)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// TestSetMigMode_DisableDestroysInstances mirrors the hardware behaviour that
// turning MIG off tears the partitioning down.
func TestSetMigMode_DisableDestroysInstances(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	createSpanningCI(t, createOneSliceGI(t, dev))

	_, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	_, ret = dev.SetMigMode(nvml.DEVICE_MIG_DISABLE)
	require.Equal(t, nvml.SUCCESS, ret)

	_, ret = dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// TestDeclaredPartitions checks that a profile can boot already partitioned,
// which is what lets the device plugin find MIG devices without anything
// having called into NVML to create them.
func TestDeclaredPartitions(t *testing.T) {
	t.Parallel()

	cfg := a100MIGConfig()
	cfg.MIG.ModeCurrent = "enabled"
	cfg.MIG.ModePending = "enabled"
	cfg.MIG.GPUInstances = []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}}

	dev := newTestDeviceWithConfig(t, cfg)

	for i := range 7 {
		migDev, ret := dev.GetMigDeviceHandleByIndex(i)
		require.Equal(t, nvml.SUCCESS, ret, "declared MIG device %d", i)

		name, ret := migDev.GetName()
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, "NVIDIA A100-SXM4-40GB MIG 1g.5gb", name)
	}

	_, ret := dev.GetMigDeviceHandleByIndex(7)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// TestDeclaredPartitions_UniformAttributes is the property the device plugin
// checks before it will accept a node with migStrategy=single.
func TestDeclaredPartitions_UniformAttributes(t *testing.T) {
	t.Parallel()

	cfg := a100MIGConfig()
	cfg.MIG.ModeCurrent = "enabled"
	cfg.MIG.ModePending = "enabled"
	cfg.MIG.GPUInstances = []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}}

	dev := newTestDeviceWithConfig(t, cfg)

	var want nvml.DeviceAttributes
	for i := range 7 {
		migDev, ret := dev.GetMigDeviceHandleByIndex(i)
		require.Equal(t, nvml.SUCCESS, ret)
		attrs, ret := migDev.GetAttributes()
		require.Equal(t, nvml.SUCCESS, ret)
		if i == 0 {
			want = attrs
			continue
		}
		require.Equal(t, want, attrs, "MIG device %d must match device 0", i)
	}
}

// TestDeclaredPartitions_IgnoredWhileMigDisabled keeps the layout inert on the
// stock profiles, which declare a partitioning but ship with MIG off.
func TestDeclaredPartitions_IgnoredWhileMigDisabled(t *testing.T) {
	t.Parallel()

	cfg := a100MIGConfig()
	cfg.MIG.GPUInstances = []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}}

	dev := newTestDeviceWithConfig(t, cfg)

	_, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

// TestDeclaredPartitions_MixedProfiles covers a heterogeneous layout, which is
// legal on hardware even though migStrategy=single rejects it.
//
// The layout also shows why placement spans matter: a 3-slice instance takes
// four grid slots, so 3g + 2g leaves room for exactly one more 1g even though
// only five of the seven compute slices are spoken for.
func TestDeclaredPartitions_MixedProfiles(t *testing.T) {
	t.Parallel()

	cfg := a100MIGConfig()
	cfg.MIG.ModeCurrent = "enabled"
	cfg.MIG.ModePending = "enabled"
	cfg.MIG.GPUInstances = []MIGGPUInstanceConfig{
		{Profile: "3g.20gb"},
		{Profile: "2g.10gb"},
		{Profile: "1g.5gb"},
	}

	dev := newTestDeviceWithConfig(t, cfg)

	names := make([]string, 0, 3)
	for i := range 3 {
		migDev, ret := dev.GetMigDeviceHandleByIndex(i)
		require.Equal(t, nvml.SUCCESS, ret, "declared MIG device %d", i)
		name, ret := migDev.GetName()
		require.Equal(t, nvml.SUCCESS, ret)
		names = append(names, name)
	}

	require.ElementsMatch(t, []string{
		"NVIDIA A100-SXM4-40GB MIG 3g.20gb",
		"NVIDIA A100-SXM4-40GB MIG 2g.10gb",
		"NVIDIA A100-SXM4-40GB MIG 1g.5gb",
	}, names)

	_, ret := dev.GetMigDeviceHandleByIndex(3)
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
}

func TestValidateMIGConfig(t *testing.T) {
	t.Parallel()

	profileID := 0
	negative := -1

	tests := []struct {
		name    string
		mig     *MIGConfig
		wantErr string
	}{
		{"absent", nil, ""},
		{"modes only", &MIGConfig{ModeCurrent: "enabled", ModePending: "enabled"}, ""},
		{"named partition", &MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}}}, ""},
		{"raw profile id", &MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{ProfileID: &profileID}}}, ""},
		{
			"nested compute instances",
			&MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{
				Profile:          "3g.20gb",
				ComputeInstances: []MIGComputeInstanceConfig{{Profile: "1c", Count: 3}},
			}}},
			"",
		},

		{"bad mode", &MIGConfig{ModeCurrent: "on"}, "mode_current"},
		{"bad pending mode", &MIGConfig{ModePending: "yes"}, "mode_pending"},
		{"negative ceiling", &MIGConfig{MaxGPUInstances: -1}, "max_gpu_instances"},
		{
			"partition names nothing",
			&MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{Count: 1}}},
			"gpu_instances[0]: must set either profile or profile_id",
		},
		{
			"partition names both",
			&MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{Profile: "1g.5gb", ProfileID: &profileID}}},
			"use one",
		},
		{
			"negative profile id",
			&MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{ProfileID: &negative}}},
			"profile_id cannot be negative",
		},
		{
			"negative count",
			&MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: -1}}},
			"count cannot be negative",
		},
		{
			"bad compute instance",
			&MIGConfig{GPUInstances: []MIGGPUInstanceConfig{{
				Profile:          "3g.20gb",
				ComputeInstances: []MIGComputeInstanceConfig{{}},
			}}},
			"gpu_instances[0].compute_instances[0]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMIGConfig(tt.mig)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestPerDeviceMIGOverride covers a gap that predates MIG support: mig was
// missing from the per-device override merge, so a devices[] entry that named
// its own MIG layout was silently dropped in favour of the defaults.
func TestPerDeviceMIGOverride(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		NumDevices: 2,
		YAMLConfig: &YAMLConfig{
			DeviceDefaults: DeviceConfig{
				Name: "NVIDIA A100-SXM4-40GB",
				MIG:  &MIGConfig{ModeCurrent: "disabled", MaxGPUInstances: 7},
			},
			Devices: []DeviceOverride{{
				Index: 1,
				DeviceConfig: DeviceConfig{
					MIG: &MIGConfig{
						ModeCurrent:     "enabled",
						ModePending:     "enabled",
						MaxGPUInstances: 7,
						GPUInstances:    []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}},
					},
				},
			}},
		},
	}

	require.Equal(t, "disabled", cfg.GetDeviceConfig(0).MIG.ModeCurrent)

	overridden := cfg.GetDeviceConfig(1).MIG
	require.Equal(t, "enabled", overridden.ModeCurrent)
	require.Len(t, overridden.GPUInstances, 1)
	require.Equal(t, 7, overridden.GPUInstances[0].Count)
}

// TestMigProfileNameForDevice resolves a name against a device's own tables,
// which is how a declared partition's profile string becomes NVML profile IDs.
func TestMigProfileNameForDevice(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())

	giID, ciID, err := dev.resolveMigProfileByName("1g.5gb")
	require.NoError(t, err)
	require.Equal(t, nvml.GPU_INSTANCE_PROFILE_1_SLICE, giID)
	require.Equal(t, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, ciID)

	giID, ciID, err = dev.resolveMigProfileByName("1c.3g.20gb")
	require.NoError(t, err)
	require.Equal(t, nvml.GPU_INSTANCE_PROFILE_3_SLICE, giID)
	require.Equal(t, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, ciID)

	_, _, err = dev.resolveMigProfileByName("9g.99gb")
	require.Error(t, err, "a profile the board does not offer must fail loudly")
}
