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
	"reflect"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
)

// MarkMIGDirty asks the next refresh to reconcile MIG state even though the
// override document has not changed.
//
// It exists for the one way a board can drift from the document without the
// document moving: a mutation this process applied to the board but could not
// record. The file is authoritative, so the board has to be pulled back to it,
// and neither of the two signals a refresh normally acts on — a new generation
// or a config that differs from the one last applied — has anything to report.
//
// The reconcile is deferred to the next refresh rather than run here because
// it destroys instances, and the caller is inside the NVML call that failed:
// a consumer's handles should not be invalidated from under it while it is
// still being told its own mutation was refused.
func (d *ConfigurableDevice) MarkMIGDirty() {
	d.migDirty.Store(true)
}

// reconcileMIG brings the board's partitioning to the effective MIG config
// when that config changed, which is what makes a runtime repartition visible
// to a consumer that is already running.
//
// Requires d.refreshMu. Every MIG getter triggers a refresh through its handle
// guard, so this runs with refreshes in flight around it: the NVML calls it
// makes re-enter refresh on this goroutine (where TryLock turns them into
// no-ops), and readers on other goroutines observe the board mid-change.
// Nothing here may assume it is the only refresh running.
func (d *ConfigurableDevice) reconcileMIG(cfg *MIGConfig) {
	// Swapped, not read: a device marked dirty owes the document one pass even
	// against the config it last applied, and clearing the mark here is what
	// keeps that from repeating on every refresh afterwards.
	if !d.migDirty.Swap(false) && reflect.DeepEqual(d.appliedMIG, cfg) {
		return
	}
	if d.recordMIGIfExempt(cfg) {
		return
	}

	st := d.migState
	current, pending := migModesOf(cfg)

	st.mu.Lock()
	sameMode := st.mode == current
	st.mu.Unlock()

	// Only a board that was and remains MIG-enabled under a recorded layout can
	// be brought to that layout by difference. Switching MIG destroys every
	// instance on hardware, and a declared (count) layout names no identities
	// for a diff to match survivors on, so both take the wholesale path.
	if !sameMode || current != nvml.DEVICE_MIG_ENABLE || cfg == nil || cfg.Instances == nil {
		d.rebuildMIG(cfg, current, pending)
		return
	}

	st.mu.Lock()
	st.pending = pending
	st.maxGPUInstances = resolveMaxGPUInstances(cfg, st.supported, st.profiles)
	st.mu.Unlock()

	d.reconcileExplicitInstances(*cfg.Instances)
	d.appliedMIG = cfg
}

// rebuildMIG is the wholesale path: tear the board down and lay it out again.
// It is what a mode change and a declared (count) layout both need, because
// neither carries the identities that would let a diff preserve anything. It
// is also how a declared layout stays a desired state, which is how
// `nvidia-mig-parted apply` behaves.
//
// Requires d.refreshMu, under the same concurrency discipline as reconcileMIG.
func (d *ConfigurableDevice) rebuildMIG(cfg *MIGConfig, current, pending int) {
	st := d.migState
	st.mu.Lock()
	retired := make([]*ConfigurableDevice, 0, len(st.devices))
	for _, migDev := range st.devices {
		retired = append(retired, migDev)
	}
	st.destroyAllLocked(d)
	st.mode, st.pending = current, pending
	st.maxGPUInstances = resolveMaxGPUInstances(cfg, st.supported, st.profiles)
	enabled := st.mode == nvml.DEVICE_MIG_ENABLE
	st.mu.Unlock()

	// Outside the lock: creating instances takes st.mu itself.
	if enabled {
		d.applyMIGLayout(cfg)
	}
	d.appliedMIG = cfg

	if d.onRepartition != nil {
		d.onRepartition(retired)
	}
	debugLog("[MIG] device %d: repartitioned from an override (enabled=%v, retired %d MIG devices)\n",
		d.index, enabled, len(retired))
}

// reconcileExplicitInstances brings the board to the recorded layout by
// difference, at the GPU- and the compute-instance level both: create what is
// missing, destroy what is no longer listed, and leave everything else
// untouched.
//
// Touching only the difference is what lets two processes mutate the same
// board: one adding an instance must not invalidate the handles the other
// holds to instances it did not touch, nor renumber them. It also makes a
// process's own write-back a no-op, since by the time the watch fires the
// board already matches the file.
//
// Requires d.refreshMu and must not be called with st.mu held: both the
// destroy and the create path take st.mu themselves.
func (d *ConfigurableDevice) reconcileExplicitInstances(records []MIGGPUInstanceRecord) {
	want := gpuInstanceRecordsByID(records)

	st := d.migState
	// liveGpuInstances guards the instance tree for itself and returns an
	// ID-ordered snapshot, so the teardown below is deterministic.
	live := make(map[uint32]struct{})
	var retired []*ConfigurableDevice
	for _, gi := range st.liveGpuInstances(d) {
		stillLive, retiredDevices := d.reconcileLiveInstance(gi, want)
		if stillLive {
			live[gi.Info.Id] = struct{}{}
		}
		retired = append(retired, retiredDevices...)
	}

	// Destroy before create, so a record can claim the slices an instance
	// leaving the layout has just freed.
	missing := make([]MIGGPUInstanceRecord, 0, len(records))
	for _, rec := range records {
		if _, exists := live[rec.ID]; !exists {
			missing = append(missing, rec)
		}
	}
	d.applyExplicitPartitions(missing)

	if len(retired) > 0 && d.onRepartition != nil {
		d.onRepartition(retired)
	}
}

// gpuInstanceRecordsByID indexes a layout by instance ID. The first record
// wins for a repeated ID, as in applyExplicitPartitions, which is what
// materializes the records the diff finds missing.
func gpuInstanceRecordsByID(records []MIGGPUInstanceRecord) map[uint32]MIGGPUInstanceRecord {
	byID := make(map[uint32]MIGGPUInstanceRecord, len(records))
	for _, rec := range records {
		if _, duplicate := byID[rec.ID]; duplicate {
			continue
		}
		byID[rec.ID] = rec
	}
	return byID
}

// reconcileLiveInstance reconciles one live GPU instance against the recorded
// layout, reporting whether it is still live afterwards and which MIG devices
// the teardowns retired. An instance the layout no longer records, or one a
// record replaces, is destroyed; the caller recreates the latter.
//
// Requires d.refreshMu and must not be called with st.mu held.
func (d *ConfigurableDevice) reconcileLiveInstance(
	gi *mockserver.GpuInstance, want map[uint32]MIGGPUInstanceRecord,
) (bool, []*ConfigurableDevice) {
	id := gi.Info.Id
	rec, wanted := want[id]

	var retired []*ConfigurableDevice
	if wanted {
		kept, retiredCIs := d.reconcileSurvivingInstance(gi, rec)
		if kept {
			return true, retiredCIs
		}
		retired = retiredCIs
	}

	st := d.migState
	// The MIG devices have to be named before the teardown evicts them from
	// the cache, exactly as GpuInstanceDestroy does it.
	derived := st.devicesDerivedFrom(id, nil)
	if ret := gi.Destroy(); ret != nvml.SUCCESS {
		warnLog("[MIG] device %d: cannot destroy GPU instance %d: %v\n", d.index, id, ret)
		// Reporting it as live is what keeps its record from being
		// materialized again under an ID that is still taken.
		return true, retired
	}

	reason := "no longer in the layout"
	if wanted {
		reason = "replaced by its record"
	}
	debugLog("[MIG] device %d: destroyed GPU instance %d (%s)\n", d.index, id, reason)
	return false, append(retired, derived...)
}

// reconcileSurvivingInstance brings a live GPU instance to the record sharing
// its ID and reports whether it was kept, together with the MIG devices any
// compute-instance teardown retired.
//
// A record whose profile or placement no longer matches is not an edit of the
// live instance: hardware can neither resize nor move a GPU instance in place,
// so such a record describes a new instance that happens to reuse an ID, and
// the caller has to replace the live one with it.
//
// Requires d.refreshMu and must not be called with st.mu held.
func (d *ConfigurableDevice) reconcileSurvivingInstance(
	gi *mockserver.GpuInstance, rec MIGGPUInstanceRecord,
) (bool, []*ConfigurableDevice) {
	giProfileID, defaultCIProfileID, err := d.resolveDeclaredGpuInstanceProfile(
		MIGGPUInstanceConfig{Profile: rec.Profile, ProfileID: rec.ProfileID})
	if err != nil {
		// A record nothing can be made of is no ground to tear a live instance
		// down: recreating it from that same record would fail as well, so the
		// board would lose a partition a consumer may be using.
		warnLog("[MIG] device %d: instance %d: %v\n", d.index, rec.ID, err)
		return true, nil
	}

	if giProfileID != int(gi.Info.ProfileId) {
		debugLog("[MIG] device %d: GPU instance %d recorded under profile %d, live under %d\n",
			d.index, rec.ID, giProfileID, gi.Info.ProfileId)
		return false, nil
	}
	// A record naming no placement accepts whichever one the instance holds.
	if rec.PlacementStart != nil && *rec.PlacementStart != int(gi.Info.Placement.Start) {
		debugLog("[MIG] device %d: GPU instance %d recorded at offset %d, live at %d\n",
			d.index, rec.ID, *rec.PlacementStart, gi.Info.Placement.Start)
		return false, nil
	}

	return true, d.reconcileComputeInstances(gi, giProfileID, defaultCIProfileID, rec.ComputeInstances)
}

// reconcileComputeInstances brings a surviving GPU instance's compute
// instances to the ones its record lists, by the same difference the GPU
// instances themselves are reconciled by, and returns the MIG devices the
// teardowns retired. A compute instance is created or destroyed on its own, so
// one of them changing must not cost a consumer the handles it holds to the
// others.
//
// A nil list is a record that says nothing about compute instances, not one
// that says there are none: the pass that created this instance from the same
// record gave it the spanning default, so reading nil as "none" would delete
// that default on the next pass. An unspecified list therefore leaves whatever
// is live in place — which on a freshly created instance is nothing, so the
// default the create path fills in still stands.
//
// Requires d.refreshMu and must not be called with st.mu held: the create and
// destroy paths take st.mu themselves.
func (d *ConfigurableDevice) reconcileComputeInstances(
	gi *mockserver.GpuInstance, giProfileID, defaultCIProfileID int,
	records *[]MIGComputeInstanceRecord,
) []*ConfigurableDevice {
	if records == nil {
		return nil
	}

	// Indexed rather than reduced to a membership set so a survivor can be
	// compared against its own record. The first record wins for a repeated
	// ID, as in applyExplicitComputeInstances, which materializes the ones the
	// diff finds missing.
	want := make(map[uint32]MIGComputeInstanceRecord, len(*records))
	for _, rec := range *records {
		if _, duplicate := want[rec.ID]; duplicate {
			continue
		}
		want[rec.ID] = rec
	}

	st := d.migState
	giID := gi.Info.Id
	live := make(map[uint32]struct{})
	var retired []*ConfigurableDevice
	for _, ci := range liveComputeInstances(gi) {
		ciID := ci.Info.Id
		if rec, keep := want[ciID]; keep {
			live[ciID] = struct{}{}
			d.reportComputeProfileDrift(gi, giProfileID, ci, rec)
			continue
		}
		// Named before the teardown evicts it, as in ComputeInstanceDestroy.
		derived := st.devicesDerivedFrom(giID, &ciID)
		if ret := ci.Destroy(); ret != nvml.SUCCESS {
			warnLog("[MIG] device %d: cannot destroy compute instance %d of GPU instance %d: %v\n",
				d.index, ciID, giID, ret)
			// Reporting it as live is what keeps a record under the same ID
			// from being materialized again on top of an instance that is
			// still there, as at the GPU instance level.
			live[ciID] = struct{}{}
			continue
		}
		retired = append(retired, derived...)
		debugLog("[MIG] device %d: destroyed compute instance %d of GPU instance %d (no longer in the layout)\n",
			d.index, ciID, giID)
	}

	// Destroy before create, so a record can claim compute slices an instance
	// just leaving the layout has freed.
	missing := make([]MIGComputeInstanceRecord, 0, len(*records))
	for _, rec := range *records {
		if _, exists := live[rec.ID]; !exists {
			missing = append(missing, rec)
		}
	}
	d.applyExplicitComputeInstances(gi, giProfileID, defaultCIProfileID, &missing)

	return retired
}

// reportComputeProfileDrift diagnoses a compute instance the layout keeps
// under a profile it is not live under.
//
// A compute instance survives on its ID alone, so a record that reuses an ID
// while changing the profile is a no-op. That is deliberate — unlike a GPU
// instance, whose replacement rule this level does not share — but the
// documents this reconciler reads are hand-editable, and an operator whose
// edit did nothing needs somewhere to see why.
//
// Requires d.refreshMu and must not be called with st.mu held: resolving a
// profile by name reads the device's memory info.
func (d *ConfigurableDevice) reportComputeProfileDrift(
	gi *mockserver.GpuInstance, giProfileID int, ci *mockserver.ComputeInstance, rec MIGComputeInstanceRecord,
) {
	ciProfileID, err := d.resolveDeclaredComputeInstanceProfile(giProfileID,
		MIGComputeInstanceConfig{Profile: rec.Profile, ProfileID: rec.ProfileID})
	if err != nil {
		warnLog("[MIG] device %d: compute instance %d of GPU instance %d: %v\n",
			d.index, rec.ID, gi.Info.Id, err)
		return
	}
	if ciProfileID != int(ci.Info.ProfileId) {
		debugLog("[MIG] device %d: compute instance %d of GPU instance %d recorded under profile %d,"+
			" live under %d and left in place\n",
			d.index, rec.ID, gi.Info.Id, ciProfileID, ci.Info.ProfileId)
	}
}

// recordMIGIfExempt records cfg as applied on a device that cannot be
// repartitioned and reports whether it did, so reconcileMIG skips the rebuild
// path for it. Recording is what keeps such a device from re-entering the
// reconciler on every later refresh.
func (d *ConfigurableDevice) recordMIGIfExempt(cfg *MIGConfig) bool {
	// A MIG device is a leaf partition; it has no migState and cannot be
	// subdivided further, so there is nothing to reconcile here.
	if d.mig != nil {
		d.appliedMIG = cfg
		return true
	}
	st := d.migState
	if st == nil || !st.supported {
		// A pending-only enable is an override the operator still expects to
		// take effect, so it earns the same diagnostic as a current one.
		if current, pending := migModesOf(cfg); current == nvml.DEVICE_MIG_ENABLE || pending == nvml.DEVICE_MIG_ENABLE {
			warnLog("[MIG] device %d: ignoring a MIG override on a board that is not MIG-capable\n", d.index)
		}
		d.appliedMIG = cfg
		return true
	}
	return false
}

// migModesOf maps a MIG config's mode strings onto NVML's mode constants. An
// absent block reads as MIG off, so removing the block from overrides.yaml
// returns the board to its profile's state rather than freezing the last
// override.
//
// A nil config must keep mapping to the disabled mode. Callers read an enabled
// mode as proof that cfg is non-nil and go on to dereference it for the layout,
// so reporting a nil config as enabled would turn that read into a nil
// dereference.
func migModesOf(cfg *MIGConfig) (current, pending int) {
	if cfg == nil {
		return nvml.DEVICE_MIG_DISABLE, nvml.DEVICE_MIG_DISABLE
	}
	if cfg.ModeCurrent == migModeEnabled {
		current = nvml.DEVICE_MIG_ENABLE
	}
	if cfg.ModePending == migModeEnabled {
		pending = nvml.DEVICE_MIG_ENABLE
	}
	return current, pending
}
