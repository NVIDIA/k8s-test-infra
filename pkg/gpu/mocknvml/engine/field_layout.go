// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// newLayoutMinDriver is the first driver known to number a block of NVML field
// ids the way the CUDA 13.3 header does. NVML renumbered that block inside the
// 580 branch: the cutover lies between 580.65 and 580.95, and 580.95.05 is the
// only version confirmed on real hardware (an AWS GB300), so drivers in
// between keep the 580.65 layout until one is confirmed.
const newLayoutMinDriver = "580.95.05"

// The renumbered block, in the newer layout. The throttle counters moved up
// and power smoothing moved down; the two ids after the counters carry power
// sync balancing, which the mock does not simulate.
const (
	newLayoutBlockFirst        = 251 // power smoothing starts here
	newLayoutSmoothingLast     = 268
	newLayoutThrottleFirst     = 269 // SW thermal, HW thermal, HW power brake
	newLayoutThrottleLast      = 271
	newLayoutSyncBalancingLast = 273

	// Distance between a newer-layout id and the 580.65 id the engine serves:
	// smoothing is added to, the throttle counters are subtracted from.
	newLayoutSmoothingShift = fiPwrSmoothingEnabled - newLayoutBlockFirst
	newLayoutThrottleShift  = newLayoutThrottleFirst - fiClocksEventSwThermSlowdown
)

// CanonicalFieldID translates a field id a consumer asked for into the 580.65
// layout the engine serves, according to the layout the driver uses. The
// second return is false for an id the driver defines but the mock does not
// simulate, which must be declined rather than answered by whichever feature
// holds that id in the canonical layout.
func CanonicalFieldID(fieldID uint32, driverVersion string) (uint32, bool) {
	if CompareDriverVersions(driverVersion, newLayoutMinDriver) < 0 {
		return fieldID, true
	}
	switch {
	case fieldID >= newLayoutBlockFirst && fieldID <= newLayoutSmoothingLast:
		return fieldID + newLayoutSmoothingShift, true
	case fieldID >= newLayoutThrottleFirst && fieldID <= newLayoutThrottleLast:
		return fieldID - newLayoutThrottleShift, true
	case fieldID > newLayoutThrottleLast && fieldID <= newLayoutSyncBalancingLast:
		return 0, false
	default:
		return fieldID, true
	}
}
