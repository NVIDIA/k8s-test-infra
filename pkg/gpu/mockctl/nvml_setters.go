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

package mockctl

// Persisting the writes NVML setters make, for the same reason ResetDevice
// exists: the mock library serves them from a CGo callback with no command
// dispatcher around it.
//
// On real hardware these are driver-level writes visible to the whole node.
// Every consumer loads its own copy of the mock, so holding them in process
// memory made a read-after-write across two processes report the old value —
// `nvidia-smi -pl` followed by a separate `nvidia-smi` being the obvious case.
// Routing them through the override document puts them in the only state the
// processes share.

import (
	"errors"
	"fmt"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// errNoOverrideDocument reports a mock with nowhere to persist to. Unlike a
// reset, which has nothing to do when no state was ever injected, a setter that
// cannot record its write has failed and must say so.
var errNoOverrideDocument = errors.New("no override document configured")

// SetPowerLimit persists a power cap for one device. The engine still clamps
// the value to the profile's [min_limit_mw, max_limit_mw] envelope on read, so
// callers that need the clamped value must read it back.
func SetPowerLimit(path string, index int, milliwatts uint32) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, PowerLimitPatch(milliwatts))
		return nil
	})
}

// UpdateWorkloadProfiles persists a new requested workload power profile list
// for one device, computed by apply from the list already on disk.
//
// The caller supplies apply rather than a finished list because the NVML
// operations are read-modify-write — SET adds to the request — and the base has
// to be read under the same lock as the write, or two concurrent callers each
// compute from the same base and one update is lost.
//
// present distinguishes "nothing has been written" from "written, then
// cleared". Only the former may fall back to the request configured in the
// profile; the latter is a caller that deliberately asked for none.
func UpdateWorkloadProfiles(
	path string, index int, apply func(base []uint32, present bool) ([]uint32, error),
) error {
	return mutateDevice(path, func(d *Doc) error {
		next, err := apply(d.WorkloadRequestedProfiles(index))
		if err != nil {
			return err
		}
		d.SetFields(Target{Index: index}, WorkloadProfilesPatch(next))
		return nil
	})
}

// SetNvlinkBwMode persists an NVLink Reduced Bandwidth Mode. allDevices routes
// the write to the `all:` bucket, which is how the node-wide NVML pair records
// a mode that belongs to the node rather than to one GPU.
//
// A node-wide write also drops the mode any per-device write left behind, so
// that every GPU reports the new one. On real hardware the node-wide call moves
// the whole node, and the per-device field outranks `all:` in the merge — so
// without this an earlier per-device set would keep masking a later node-wide
// one. Only the bandwidth mode is cleared; the rest of a device's injected
// state has nothing to do with this API.
func SetNvlinkBwMode(path string, index int, mode uint8, allDevices bool) error {
	return mutateDevice(path, func(d *Doc) error {
		if allDevices {
			d.ClearDeviceField(nvlinkBwModeKey)
		}
		d.SetFields(nvlinkTarget(index, allDevices), NvlinkBwModePatch(mode))
		return nil
	})
}

// SetNvlinkLowPowerThreshold persists an NVLink low-power threshold for one
// device. A nil threshold removes the recorded value so the device falls back
// to the driver default, which is what the setter's reset sentinel means.
func SetNvlinkLowPowerThreshold(path string, index int, threshold *uint32) error {
	return mutateDevice(path, func(d *Doc) error {
		if threshold == nil {
			d.ClearField(Target{Index: index}, nvlinkLowPowerThresholdKey)
			return nil
		}
		d.SetFields(Target{Index: index}, NvlinkLowPowerThresholdPatch(*threshold))
		return nil
	})
}

// nvlinkTarget selects the bucket an NVLink write lands in.
func nvlinkTarget(index int, allDevices bool) Target {
	if allDevices {
		return Target{All: true}
	}
	return Target{Index: index}
}

// mutateDevice runs mutate against the document under the flock every writer of
// this file takes, re-reading inside the lock so a concurrent injection cannot
// interleave. A mutate that returns an error leaves the file untouched.
func mutateDevice(path string, mutate func(*Doc) error) error {
	if path == "" {
		return errNoOverrideDocument
	}

	unlock, err := LockOverride(path)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlock()

	doc, err := Load(path)
	if err != nil {
		return err
	}
	if err := mutate(doc); err != nil {
		return err
	}
	return WriteAtomic(path, doc)
}

// PowerLimitPatch builds an override patch that caps the device at milliwatts,
// the state nvmlDeviceSetPowerManagementLimit leaves behind.
func PowerLimitPatch(milliwatts uint32) map[string]any {
	return map[string]any{
		"power": map[string]any{"enforced_limit_mw": milliwatts},
	}
}

// WorkloadProfilesPatch builds an override patch that replaces the requested
// profile list. A nil list is normalised to an empty one: nil marshals to null,
// which reads back as "nothing was ever requested" and would make a caller who
// cleared every profile indistinguishable from one who never wrote at all.
func WorkloadProfilesPatch(requested []uint32) map[string]any {
	if requested == nil {
		requested = []uint32{}
	}
	return map[string]any{
		"power": map[string]any{
			"workload_power_profiles": map[string]any{"requested": requested},
		},
	}
}

// nvlinkLowPowerThresholdKey is the override field the low-power setter writes,
// named once because the reset path deletes the same key the set path writes.
const nvlinkLowPowerThresholdKey = "nvlink_low_power_threshold"

// nvlinkBwModeKey is the override field both bandwidth-mode setters write,
// named once because a node-wide write deletes from the per-device buckets the
// same key a per-device write records.
const nvlinkBwModeKey = "nvlink_bw_mode"

// NvlinkBwModePatch builds an override patch recording the bandwidth mode
// nvmlDeviceSetNvlinkBwMode or nvmlSystemSetNvlinkBwMode applied.
func NvlinkBwModePatch(mode uint8) map[string]any {
	return map[string]any{nvlinkBwModeKey: mode}
}

// NvlinkLowPowerThresholdPatch builds an override patch recording the threshold
// nvmlDeviceSetNvLinkDeviceLowPowerThreshold applied.
func NvlinkLowPowerThresholdPatch(threshold uint32) map[string]any {
	return map[string]any{nvlinkLowPowerThresholdKey: threshold}
}

// WorkloadRequestedProfiles reports the profiles requested of device index by a
// previous setter, and whether any setter has written at all.
//
// Like FailureXid it resolves through the engine rather than reading the raw
// document, so the answer is what the running mock would act on: the merge is a
// deep one, and a per-device request does not replace a shared `all:` one.
func (d *Doc) WorkloadRequestedProfiles(index int) (requested []uint32, present bool) {
	overrides := &engine.ConfigOverrideDoc{All: d.All, Devices: d.Devices}

	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, overrides.DeviceConfigOverride(index))
	if err != nil || merged.Power == nil || merged.Power.WorkloadProfiles == nil {
		return nil, false
	}
	// Distinguishing nil from empty is the whole point: an empty list is a
	// caller who cleared everything, nil is a document that never carried one.
	if merged.Power.WorkloadProfiles.Requested == nil {
		return nil, false
	}
	return merged.Power.WorkloadProfiles.Requested, true
}
