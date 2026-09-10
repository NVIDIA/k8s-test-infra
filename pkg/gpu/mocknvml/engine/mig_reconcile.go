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
)

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
	if reflect.DeepEqual(d.appliedMIG, cfg) {
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
// difference: create what is missing, destroy what is no longer listed, and
// leave everything else untouched.
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
	want := make(map[uint32]struct{}, len(records))
	for _, rec := range records {
		want[rec.ID] = struct{}{}
	}

	st := d.migState
	// liveGpuInstances guards the instance tree for itself and returns an
	// ID-ordered snapshot, so the teardown below is deterministic.
	live := make(map[uint32]struct{})
	var retired []*ConfigurableDevice
	for _, gi := range st.liveGpuInstances(d) {
		id := gi.Info.Id
		if _, keep := want[id]; keep {
			live[id] = struct{}{}
			continue
		}
		// The MIG devices have to be named before the teardown evicts them
		// from the cache, exactly as GpuInstanceDestroy does it.
		derived := st.devicesDerivedFrom(id, nil)
		if ret := gi.Destroy(); ret != nvml.SUCCESS {
			warnLog("[MIG] device %d: cannot destroy GPU instance %d: %v\n", d.index, id, ret)
			continue
		}
		retired = append(retired, derived...)
		debugLog("[MIG] device %d: destroyed GPU instance %d (no longer in the layout)\n", d.index, id)
	}

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
