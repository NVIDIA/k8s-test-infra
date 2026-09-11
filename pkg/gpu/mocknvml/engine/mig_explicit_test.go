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

// liveGpuInstanceIDs is what a consumer would see enumerating the board.
func liveGpuInstanceIDs(t *testing.T, dev *ConfigurableDevice) []uint32 {
	t.Helper()
	st := dev.migState
	st.mu.Lock()
	defer st.mu.Unlock()
	gis := st.liveGpuInstances(dev)
	ids := make([]uint32, 0, len(gis))
	for _, gi := range gis {
		ids = append(ids, gi.Info.Id)
	}
	return ids
}

// The layout a delete leaves behind: ids 0 and 2, with 1 gone. This is the
// case the count form cannot represent and the explicit form exists for.
func TestApplyMIGLayout_ExplicitLayoutWithAGap(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	start0, start2 := 0, 2
	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent: migModeEnabled,
		Instances: &[]MIGGPUInstanceRecord{
			{ID: 0, Profile: "1g.5gb", PlacementStart: &start0},
			{ID: 2, Profile: "1g.5gb", PlacementStart: &start2},
		},
	})

	require.Equal(t, []uint32{0, 2}, liveGpuInstanceIDs(t, dev))

	st := dev.migState
	st.mu.Lock()
	gis := st.liveGpuInstances(dev)
	st.mu.Unlock()
	require.Len(t, gis, 2)
	require.Equal(t, uint32(0), gis[0].Info.Placement.Start)
	require.Equal(t, uint32(2), gis[1].Info.Placement.Start)
}

// An empty explicit list is a MIG-enabled board with nothing on it, and must
// not fall back to the profile's declared partitions.
func TestApplyMIGLayout_EmptyExplicitListWinsOverDeclared(t *testing.T) {
	t.Parallel()

	cfg := a100MIGConfig()
	cfg.MIG.GPUInstances = []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}}
	dev := newTestDeviceWithConfig(t, cfg)
	enableMIG(t, dev)

	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent:  migModeEnabled,
		GPUInstances: cfg.MIG.GPUInstances,
		Instances:    &[]MIGGPUInstanceRecord{},
	})

	require.Empty(t, liveGpuInstanceIDs(t, dev))
}

// With no explicit list the declared counts still apply, unchanged.
func TestApplyMIGLayout_FallsBackToDeclaredCounts(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent:  migModeEnabled,
		GPUInstances: []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 3}},
	})

	require.Len(t, liveGpuInstanceIDs(t, dev), 3)
}

// Pinned creation places an instance wherever it is told without checking, so
// a layout that names one offset twice must be caught while the placement is
// resolved. Two instances sharing slices would be a board no hardware can be.
func TestApplyMIGLayout_RejectsAnOverlappingRecord(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	start := 0
	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent: migModeEnabled,
		Instances: &[]MIGGPUInstanceRecord{
			{ID: 0, Profile: "1g.5gb", PlacementStart: &start},
			{ID: 1, Profile: "1g.5gb", PlacementStart: &start},
		},
	})

	require.Equal(t, []uint32{0}, liveGpuInstanceIDs(t, dev))
}

// A config override is merged without semantic validation, so a layout can
// arrive naming the same instance twice. Both records place fine on a free
// board, and pinned creation would stamp the id on each, leaving lookups by
// id resolving to whichever instance the map yielded first.
func TestApplyMIGLayout_SkipsADuplicateInstanceID(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent: migModeEnabled,
		Instances: &[]MIGGPUInstanceRecord{
			{ID: 0, Profile: "1g.5gb"},
			{ID: 0, Profile: "1g.5gb"},
		},
	})

	require.Equal(t, []uint32{0}, liveGpuInstanceIDs(t, dev))
}

// The same unvalidated route can repeat a compute instance id within one
// record, and the mock keys compute instances on the pointer, so both would
// go live under the id.
func TestApplyMIGLayout_SkipsADuplicateComputeInstanceID(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent: migModeEnabled,
		Instances: &[]MIGGPUInstanceRecord{{
			ID: 0, Profile: "3g.20gb",
			ComputeInstances: &[]MIGComputeInstanceRecord{
				{ID: 1, Profile: "1c"},
				{ID: 1, Profile: "1c"},
			},
		}},
	})

	st := dev.migState
	st.mu.Lock()
	gis := st.liveGpuInstances(dev)
	st.mu.Unlock()
	require.Len(t, gis, 1)

	cis := liveComputeInstances(gis[0])
	require.Len(t, cis, 1)
	require.Equal(t, uint32(1), cis[0].Info.Id)
}

// Compute instances carry their own ids, so a consumer addressing ci 0 of gi 2
// reaches the same partition in every process.
func TestApplyMIGLayout_PinsComputeInstanceIDs(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent: migModeEnabled,
		Instances: &[]MIGGPUInstanceRecord{{
			ID: 2, Profile: "1g.5gb",
			ComputeInstances: &[]MIGComputeInstanceRecord{{ID: 4, Profile: "1c"}},
		}},
	})

	st := dev.migState
	st.mu.Lock()
	gis := st.liveGpuInstances(dev)
	st.mu.Unlock()
	require.Len(t, gis, 1)

	cis := liveComputeInstances(gis[0])
	require.Len(t, cis, 1)
	require.Equal(t, uint32(4), cis[0].Info.Id)
}

// A record that says nothing about compute instances gets the partitioning a
// consumer sees after creating a GPU instance without asking for slices: one
// compute instance spanning it.
func TestApplyMIGLayout_UnspecifiedComputeInstancesGetTheSpanningDefault(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent: migModeEnabled,
		Instances:   &[]MIGGPUInstanceRecord{{ID: 0, Profile: "3g.20gb"}},
	})

	st := dev.migState
	st.mu.Lock()
	gis := st.liveGpuInstances(dev)
	st.mu.Unlock()
	require.Len(t, gis, 1)

	require.Len(t, liveComputeInstances(gis[0]), 1)
}

// `nvidia-smi mig -cgi` without -C, and deleting the last compute instance,
// both leave a GPU instance carrying none. An empty list records that state,
// so it must materialize empty instead of being handed the spanning default.
func TestApplyMIGLayout_EmptyComputeInstanceListCreatesNone(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)

	dev.applyMIGLayout(&MIGConfig{
		ModeCurrent: migModeEnabled,
		Instances: &[]MIGGPUInstanceRecord{{
			ID: 0, Profile: "3g.20gb",
			ComputeInstances: &[]MIGComputeInstanceRecord{},
		}},
	})

	st := dev.migState
	st.mu.Lock()
	gis := st.liveGpuInstances(dev)
	st.mu.Unlock()
	require.Len(t, gis, 1)

	require.Empty(t, liveComputeInstances(gis[0]))
}

// The records a mutation writes down have to describe the board it is about
// to change, identities included: the delta that follows edits them by ID.
func TestMIGLayoutRecords_MirrorsTheLiveBoard(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, dev)
	first := createOneSliceGI(t, dev)
	createSpanningCI(t, first)
	createOneSliceGI(t, dev)

	records := dev.MIGLayoutRecords()

	require.Len(t, records, 2)
	require.Equal(t, uint32(0), records[0].ID)
	require.Equal(t, uint32(1), records[1].ID)
	for _, rec := range records {
		require.NotNil(t, rec.ProfileID)
		require.Equal(t, nvml.GPU_INSTANCE_PROFILE_1_SLICE, *rec.ProfileID)
		require.NotNil(t, rec.PlacementStart)
	}
	require.Equal(t, 0, *records[0].PlacementStart)
	require.Equal(t, 1, *records[1].PlacementStart)

	require.NotNil(t, records[0].ComputeInstances)
	require.Len(t, *records[0].ComputeInstances, 1)
	require.Equal(t, uint32(0), (*records[0].ComputeInstances)[0].ID)
}

// The records have to reproduce the board they were read from. A GPU instance
// with no compute instances is the case that can silently gain one: an absent
// list is how a record asks for the spanning default, so recording "none" as
// silence would hand the next process a partition nobody created.
func TestMIGLayoutRecords_RoundTripABoardWithNoComputeInstances(t *testing.T) {
	t.Parallel()

	source := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, source)
	createOneSliceGI(t, source)

	records := source.MIGLayoutRecords()

	replica := newTestDeviceWithConfig(t, a100MIGConfig())
	enableMIG(t, replica)
	replica.applyMIGLayout(&MIGConfig{ModeCurrent: migModeEnabled, Instances: &records})

	st := replica.migState
	st.mu.Lock()
	gis := st.liveGpuInstances(replica)
	st.mu.Unlock()
	require.Len(t, gis, 1)
	require.Empty(t, liveComputeInstances(gis[0]),
		"a recorded instance with no compute instances must come back with none")
}

// A board that is not partitioned has no layout rather than an empty one:
// recording an empty list would say every instance was destroyed, which for a
// board whose partitions its profile declares is a very different document.
func TestMIGLayoutRecords_UnpartitionableBoardHasNoLayout(t *testing.T) {
	t.Parallel()

	dev := newTestDeviceWithConfig(t, a100MIGConfig())

	require.Nil(t, dev.MIGLayoutRecords(), "MIG is off on this board")

	enableMIG(t, dev)
	require.NotNil(t, dev.MIGLayoutRecords(), "an enabled board with nothing on it has an empty layout")
	require.Empty(t, dev.MIGLayoutRecords())
}
