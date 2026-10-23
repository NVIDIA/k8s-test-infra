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

// SetPersistenceMode persists whether persistence mode is enabled on one device.
func SetPersistenceMode(path string, index int, enabled bool) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, map[string]any{"persistence_mode": enableState(enabled)})
		return nil
	})
}

// SetDrainState persists the drain state nvmlDeviceModifyDrainState applied.
func SetDrainState(path string, index int, draining bool) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, map[string]any{"draining": draining})
		return nil
	})
}

// SetRemoved persists whether a GPU was removed from the driver, the state
// nvmlDeviceRemoveGpu sets and nvmlDeviceDiscoverGpus clears.
func SetRemoved(path string, index int, removed bool) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, map[string]any{"removed": removed})
		return nil
	})
}

// SetPowerMizerMode persists the PowerMizer mode nvmlDeviceSetPowerMizerMode_v1
// applied, spelled the way the profile schema names it.
func SetPowerMizerMode(path string, index int, mode string) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, map[string]any{"power": map[string]any{"power_mizer_mode": mode}})
		return nil
	})
}

// SetPowerSmoothingState persists whether power smoothing is enabled on one
// device.
func SetPowerSmoothingState(path string, index int, enabled bool) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, powerSmoothingPatch(map[string]any{"state": enableState(enabled)}))
		return nil
	})
}

// SetPowerSmoothingActiveProfile persists the preset profile
// nvmlDevicePowerSmoothingActivatePresetProfile made active.
func SetPowerSmoothingActiveProfile(path string, index int, profileID uint32) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, powerSmoothingPatch(map[string]any{"active_profile": profileID}))
		return nil
	})
}

// SetPowerSmoothingProfileParam persists one preset profile parameter. Only
// that parameter is written, so the rest of the preset stays the profile's.
func SetPowerSmoothingProfileParam(
	path string, index int, profileID uint32, param engine.PowerSmoothingParam, value float64,
) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, engine.PowerSmoothingProfilePatch(profileID, param, value))
		return nil
	})
}

func powerSmoothingPatch(fields map[string]any) map[string]any {
	return map[string]any{"power": map[string]any{"smoothing": fields}}
}

// SetAccountingMode persists whether per-process accounting is enabled on one
// device. Disabling also records an empty terminated list, because turning
// accounting off clears every record the driver held.
func SetAccountingMode(path string, index int, enabled bool) error {
	return mutateDevice(path, func(d *Doc) error {
		patch := map[string]any{"mode": enableState(enabled)}
		if !enabled {
			patch[accountingTerminatedKey] = []any{}
		}
		d.SetFields(Target{Index: index}, map[string]any{"accounting": patch})
		return nil
	})
}

// ClearAccountingPids persists the clear nvmlDeviceClearAccountingPids applied.
// It records an empty list rather than deleting the key, because the records it
// clears may come from the profile, and only an explicit empty list outranks
// them in the merge.
func ClearAccountingPids(path string, index int) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, map[string]any{
			"accounting": map[string]any{accountingTerminatedKey: []any{}},
		})
		return nil
	})
}

// accountingTerminatedKey is the accounting field both accounting setters
// empty.
const accountingTerminatedKey = "terminated"

// enableState spells a toggle the way the profile schema does.
func enableState(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
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

// UpdateFanControl persists new per-fan control state for one device, computed
// by apply from the state already on disk.
//
// apply takes the whole list for the same reason UpdateWorkloadProfiles does:
// the NVML fan setters address one fan while the recorded state covers every
// one, so the base has to be read under the same lock as the write or two
// callers writing different fans lose one of the two writes.
func UpdateFanControl(
	path string, index int,
	apply func(base []engine.FanControlConfig, present bool) ([]engine.FanControlConfig, error),
) error {
	return mutateDevice(path, func(d *Doc) error {
		next, err := apply(d.FanControls(index))
		if err != nil {
			return err
		}
		d.SetFields(Target{Index: index}, FanControlPatch(next))
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

// SetApplicationsClocks persists the applications clocks for one device. A
// reset writes the board defaults through here too, so the pair is always
// recorded rather than cleared.
func SetApplicationsClocks(path string, index int, memMHz, graphicsMHz uint32) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, ApplicationsClocksPatch(memMHz, graphicsMHz))
		return nil
	})
}

// SetLockedClocks persists the range one clock domain is locked to. A nil
// range is the reset setters, and records an explicit unlock rather than
// deleting the device's lock: unlike a power cap, whose reset falls back to the
// profile's value, an unlocked GPU is the driver default, so a lock inherited
// from the profile or the `all:` bucket must not reappear through the merge.
func SetLockedClocks(path string, index int, domain engine.ClockDomain, r *engine.ClockRangeConfig) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, LockedClocksPatch(domain, r))
		return nil
	})
}

// SetClockOffset persists the offset applied to one clock domain.
func SetClockOffset(path string, index int, domain engine.ClockDomain, offsetMHz int32) error {
	return mutateDevice(path, func(d *Doc) error {
		d.SetFields(Target{Index: index}, ClockOffsetPatch(domain, offsetMHz))
		return nil
	})
}

// ApplicationsClocksPatch builds an override patch recording the applications
// clocks nvmlDeviceSetApplicationsClocks applied.
func ApplicationsClocksPatch(memMHz, graphicsMHz uint32) map[string]any {
	return map[string]any{
		"clocks": map[string]any{"memory_app": memMHz, "graphics_app": graphicsMHz},
	}
}

// LockedClocksPatch builds an override patch recording the range
// nvmlDeviceSetGpuLockedClocks or nvmlDeviceSetMemoryLockedClocks applied, or
// for a nil range the unlock their resets applied.
func LockedClocksPatch(domain engine.ClockDomain, r *engine.ClockRangeConfig) map[string]any {
	var lock any
	if r != nil {
		lock = map[string]any{"min_mhz": r.MinMHz, "max_mhz": r.MaxMHz}
	}
	return map[string]any{"clocks": map[string]any{lockedClocksKey(domain): lock}}
}

// ClockOffsetPatch builds an override patch recording the offset
// nvmlDeviceSetClockOffsets applied. Only offset_mhz is written: the range it
// was validated against belongs to the board and stays the profile's.
func ClockOffsetPatch(domain engine.ClockDomain, offsetMHz int32) map[string]any {
	return map[string]any{
		"clocks": map[string]any{
			"offsets": map[string]any{string(domain): map[string]any{"offset_mhz": offsetMHz}},
		},
	}
}

// lockedClocksKey is the clocks field a domain's lock is recorded under.
func lockedClocksKey(domain engine.ClockDomain) string {
	return "locked_" + string(domain)
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

// FanControlPatch builds an override patch that replaces a device's per-fan
// control state. A nil list is normalised to an empty one for the same reason
// WorkloadProfilesPatch does: nil marshals to null, which reads back as
// "nothing was ever written" and would make a caller who released every fan
// indistinguishable from one that never wrote.
func FanControlPatch(controls []engine.FanControlConfig) map[string]any {
	if controls == nil {
		controls = []engine.FanControlConfig{}
	}
	return map[string]any{"fan": map[string]any{"controls": controls}}
}

// FanControls reports the per-fan control state written for device index by a
// previous fan setter, and whether any setter has written at all.
//
// Like WorkloadRequestedProfiles it resolves through the engine rather than
// reading the raw document, so the answer is what the running mock would act
// on — including the mock refusing a malformed document, which leaves the
// device with the control state its profile declares.
func (d *Doc) FanControls(index int) (controls []engine.FanControlConfig, present bool) {
	overrides := &engine.ConfigOverrideDoc{All: d.All, Devices: d.Devices}

	merged, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, overrides.DeviceConfigOverride(index))
	if err != nil || merged.Fan == nil || merged.Fan.Controls == nil {
		return nil, false
	}
	return merged.Fan.Controls, true
}
