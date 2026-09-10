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

// reconcileMIG rebuilds the board's partitioning when the effective MIG config
// changed, which is what makes a runtime repartition visible to a consumer that
// is already running.
//
// The layout is rebuilt wholesale rather than diffed against the live one: the
// declared layout is a desired state, and re-deriving it from scratch is both
// how `nvidia-mig-parted apply` behaves on hardware and the only way the
// instance IDs stay the ones DeclaredMIGLayout predicts.
//
// Requires d.refreshMu. Every MIG getter triggers a refresh through its handle
// guard, so this rebuild runs with refreshes in flight around it: the NVML
// calls it makes re-enter refresh on this goroutine (where TryLock turns them
// into no-ops), and readers on other goroutines observe the board mid-rebuild.
// Nothing here may assume it is the only refresh running.
func (d *ConfigurableDevice) reconcileMIG(cfg *MIGConfig) {
	if reflect.DeepEqual(d.appliedMIG, cfg) {
		return
	}
	if d.reconcileMIGExempt(cfg) {
		return
	}

	st := d.migState
	st.mu.Lock()
	retired := make([]*ConfigurableDevice, 0, len(st.devices))
	for _, migDev := range st.devices {
		retired = append(retired, migDev)
	}
	st.destroyAllLocked(d)
	st.mode, st.pending = migModesOf(cfg)
	st.maxGPUInstances = resolveMaxGPUInstances(cfg, st.supported, st.profiles)
	enabled := st.mode == nvml.DEVICE_MIG_ENABLE
	st.mu.Unlock()

	// Outside the lock: creating instances takes st.mu itself.
	if enabled {
		d.applyDeclaredPartitions(cfg.GPUInstances)
	}
	d.appliedMIG = cfg

	if d.onRepartition != nil {
		d.onRepartition(retired)
	}
	debugLog("[MIG] device %d: repartitioned from an override (enabled=%v, retired %d MIG devices)\n",
		d.index, enabled, len(retired))
}

// reconcileMIGExempt records cfg for devices that cannot be repartitioned and
// returns true so reconcileMIG skips the rebuild path.
func (d *ConfigurableDevice) reconcileMIGExempt(cfg *MIGConfig) bool {
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
