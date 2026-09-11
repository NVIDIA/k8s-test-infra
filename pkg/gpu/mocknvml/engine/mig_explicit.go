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
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
)

// applyMIGLayout partitions the board from cfg, preferring the explicit
// instance list over the declared counts.
//
// Precedence is by presence, not emptiness: an empty list is a MIG-enabled
// board whose instances were all deleted, and falling back to the counts there
// would resurrect the partitions the operator just removed.
func (d *ConfigurableDevice) applyMIGLayout(cfg *MIGConfig) {
	if cfg == nil {
		return
	}
	if cfg.Instances != nil {
		d.applyExplicitPartitions(*cfg.Instances)
		return
	}
	d.applyDeclaredPartitions(cfg.GPUInstances)
}

// applyExplicitPartitions recreates exactly the instances the layout records,
// under the IDs and placements it records them with.
//
// A record that cannot be placed is logged and skipped rather than aborting
// the rest, matching how a declared layout handles an impossible entry: a
// board that comes up with most of its partitions is easier to diagnose than
// one that comes up bare.
//
// A layout can reach here from a config override, which is merged without
// semantic validation, so the uniqueness of instance IDs is enforced here:
// two live instances under one ID would make lookups by ID ambiguous.
func (d *ConfigurableDevice) applyExplicitPartitions(records []MIGGPUInstanceRecord) {
	live := make(map[uint32]struct{}, len(records))
	for _, rec := range records {
		if _, taken := live[rec.ID]; taken {
			warnLog("[MIG] device %d: instance %d already exists, skipping duplicate record\n",
				d.index, rec.ID)
			continue
		}
		giProfileID, defaultCIProfileID, err := d.resolveDeclaredGpuInstanceProfile(
			MIGGPUInstanceConfig{Profile: rec.Profile, ProfileID: rec.ProfileID})
		if err != nil {
			warnLog("[MIG] device %d: instance %d: %v\n", d.index, rec.ID, err)
			continue
		}
		info, ret := d.GetGpuInstanceProfileInfo(giProfileID)
		if ret != nvml.SUCCESS {
			warnLog("[MIG] device %d: instance %d: GPU instance profile %d unavailable: %v\n",
				d.index, rec.ID, giProfileID, ret)
			continue
		}
		gi, ret := d.createExplicitGpuInstance(&info, rec)
		if ret != nvml.SUCCESS {
			warnLog("[MIG] device %d: cannot recreate instance %d (%s): %v\n",
				d.index, rec.ID, rec.Profile, ret)
			continue
		}
		live[rec.ID] = struct{}{}
		d.applyExplicitComputeInstances(gi, giProfileID, defaultCIProfileID, rec.ComputeInstances)
	}
}

// createExplicitGpuInstance places the instance where the record says, or at
// the first free offer when the record leaves placement open.
func (d *ConfigurableDevice) createExplicitGpuInstance(
	info *nvml.GpuInstanceProfileInfo, rec MIGGPUInstanceRecord,
) (nvml.GpuInstance, nvml.Return) {
	st, ret := d.migEnabled()
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	placement, ok := st.explicitPlacementLocked(d, info, rec.PlacementStart)
	if !ok {
		return nil, nvml.ERROR_INSUFFICIENT_RESOURCES
	}
	id := rec.ID
	return st.createGpuInstancePinnedLocked(d, info, &placement, &id)
}

// explicitPlacementLocked resolves the placement for a record: the offset it
// names, or the first offered placement whose slices are free.
//
// Pinned creation performs no placement checks of its own, so this is the only
// thing standing between a layout that names overlapping offsets and a board
// that silently comes up with instances on top of each other. Requires st.mu.
func (st *migState) explicitPlacementLocked(
	parent *ConfigurableDevice, info *nvml.GpuInstanceProfileInfo, start *int,
) (nvml.GpuInstancePlacement, bool) {
	if start == nil {
		return st.freePlacementLocked(parent, int(info.Id))
	}

	occupied := st.occupiedSlicesLocked(parent)
	for _, placement := range st.profiles.GpuInstancePlacements[int(info.Id)] {
		if int(placement.Start) != *start {
			continue
		}
		if occupied&placementMask(placement) == 0 {
			return placement, true
		}
	}
	return nvml.GpuInstancePlacement{}, false
}

// applyExplicitComputeInstances recreates the compute instances a record
// carries, defaulting to one spanning the whole GPU instance when the record
// says nothing about them. A record that names no compute instances asks for a
// partition without saying how it is subdivided, and the whole of it is the
// only subdivision that answer can mean, so it materializes the same single
// spanning instance a declared partition gets.
//
// Presence, not emptiness, selects the default: an empty list is a GPU
// instance whose compute instances were never created or were all deleted,
// and giving it the default back would invent a partition nobody asked for.
func (d *ConfigurableDevice) applyExplicitComputeInstances(
	gi nvml.GpuInstance, giProfileID, defaultCIProfileID int, records *[]MIGComputeInstanceRecord,
) {
	if records == nil {
		d.applyDeclaredComputeInstances(gi, giProfileID, defaultCIProfileID, nil)
		return
	}
	live := make(map[uint32]struct{}, len(*records))
	for _, rec := range *records {
		if _, taken := live[rec.ID]; taken {
			warnLog("[MIG] device %d: compute instance %d already exists, skipping duplicate record\n",
				d.index, rec.ID)
			continue
		}
		ciProfileID, err := d.resolveDeclaredComputeInstanceProfile(giProfileID,
			MIGComputeInstanceConfig{Profile: rec.Profile, ProfileID: rec.ProfileID})
		if err != nil {
			warnLog("[MIG] device %d: compute instance %d: %v\n", d.index, rec.ID, err)
			continue
		}
		if ret := createPinnedComputeInstance(d.index, gi, ciProfileID, rec.ID); ret != nvml.SUCCESS {
			warnLog("[MIG] device %d: cannot recreate compute instance %d: %v\n", d.index, rec.ID, ret)
			continue
		}
		live[rec.ID] = struct{}{}
	}
}

// createPinnedComputeInstance creates a compute instance through the ordinary
// path and then gives it the recorded ID, the compute-instance counterpart of
// createGpuInstancePinnedLocked.
//
// Stamping after the fact is safe because the mock keys its compute-instance
// set on the pointer, never on the ID it handed out. The counter is then
// pushed past the pinned value so a later auto-assigned instance cannot
// collide with it.
func createPinnedComputeInstance(deviceIndex int, gi nvml.GpuInstance, ciProfileID int, id uint32) nvml.Return {
	mock, ok := gi.(*mockserver.GpuInstance)
	if !ok {
		return nvml.ERROR_UNKNOWN
	}
	info, ret := gi.GetComputeInstanceProfileInfo(ciProfileID, nvml.COMPUTE_INSTANCE_ENGINE_PROFILE_SHARED)
	if ret != nvml.SUCCESS {
		return ret
	}
	created, ret := gi.CreateComputeInstance(&info)
	if ret != nvml.SUCCESS {
		return ret
	}
	ci, ok := created.(*mockserver.ComputeInstance)
	if !ok {
		return nvml.ERROR_UNKNOWN
	}

	mock.Lock()
	ci.Info.Id = id
	if mock.ComputeInstanceCounter <= id {
		mock.ComputeInstanceCounter = id + 1
	}
	mock.Unlock()

	debugLog("[MIG] device %d: pinned compute instance gi=%d ci=%d profile=%d\n",
		deviceIndex, mock.Info.Id, id, ciProfileID)
	return nvml.SUCCESS
}
