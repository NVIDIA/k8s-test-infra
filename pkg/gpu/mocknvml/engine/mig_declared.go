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

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// MIGLayout is the MIG partitioning one GPU boots with, described in the terms
// the driver's capability surface is keyed by: GPU index, GPU instance ID and
// compute instance ID.
type MIGLayout struct {
	GPUIndex     int
	GPUInstances []MIGGpuInstanceLayout
}

// MIGGpuInstanceLayout is one GPU instance and the compute instances inside it.
type MIGGpuInstanceLayout struct {
	ID uint32
	// Profile is the canonical name, e.g. "1g.5gb" — the same spelling the
	// device plugin derives its nvidia.com/mig-<profile> resource from.
	Profile            string
	ComputeInstanceIDs []uint32
}

// DeclaredMIGLayout reports the partitioning a profile boots with, for callers
// outside NVML that must agree with it — chiefly the node agent, which stages
// the /dev/nvidia-caps nodes and the mig-minors table keyed by these IDs.
//
// It exists so those IDs have a single source of truth. The alternative, having
// the agent re-derive the layout from the same YAML, looks equivalent but is
// not: instance IDs come from placement allocation, so any drift between the
// two derivations would hand a consumer cap minors belonging to a different
// partition — a mismatch that would surface as a mis-scheduled pod rather than
// an error.
//
// Only devices that boot partitioned appear; a profile that declares a layout
// with MIG mode off contributes nothing, because nothing is partitioned.
func DeclaredMIGLayout(config *Config) []MIGLayout {
	if config == nil {
		return nil
	}

	// Building the devices is how the layout is computed rather than a way to
	// sample it: these are the same constructors the library runs at
	// nvmlInit, so the IDs are the ones NVML will report by construction.
	server, err := NewEngine(config).createServer()
	if err != nil {
		warnLog("[MIG] cannot compile declared layout: %v\n", err)
		return nil
	}

	var layouts []MIGLayout
	for index, dev := range server.configurableDevices {
		if dev == nil {
			continue
		}
		if instances := dev.declaredGpuInstanceLayout(); len(instances) > 0 {
			layouts = append(layouts, MIGLayout{GPUIndex: index, GPUInstances: instances})
		}
	}
	return layouts
}

// declaredGpuInstanceLayout walks this device's live partitions in the order
// GetMigDeviceHandleByIndex enumerates them, so a caller pairing the two sees
// the same partition at the same position.
func (d *ConfigurableDevice) declaredGpuInstanceLayout() []MIGGpuInstanceLayout {
	st := d.migState
	if st == nil || !st.supported {
		return nil
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.mode != nvml.DEVICE_MIG_ENABLE {
		return nil
	}

	var instances []MIGGpuInstanceLayout
	for _, gi := range st.liveGpuInstances(d) {
		computeInstances := liveComputeInstances(gi)
		ids := make([]uint32, 0, len(computeInstances))
		for _, ci := range computeInstances {
			ids = append(ids, ci.Info.Id)
		}
		instances = append(instances, MIGGpuInstanceLayout{
			ID:                 gi.Info.Id,
			Profile:            st.gpuInstanceProfileNameLocked(d, int(gi.Info.ProfileId)),
			ComputeInstanceIDs: ids,
		})
	}
	return instances
}

// gpuInstanceProfileNameLocked spells a GPU instance profile the way the
// cluster names it. The name is that of a compute instance spanning the whole
// GPU instance, which is how a partition is named when it is not subdivided.
// Requires st.mu.
func (st *migState) gpuInstanceProfileNameLocked(parent *ConfigurableDevice, giProfileID int) string {
	ciProfileID, err := spanningComputeInstanceProfile(giProfileID)
	if err != nil {
		return ""
	}
	name, err := migProfileName(
		giProfileID, ciProfileID,
		st.profiles.GpuInstanceProfiles[giProfileID].MemorySizeMB,
		parent.memoryInfo().Total,
	)
	if err != nil {
		return ""
	}
	return name
}
