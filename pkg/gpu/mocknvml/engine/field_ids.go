// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Field ids are part of the driver's ABI, and the 590 branch renumbered a
// block of them: its header (NVML 13.1) moved power smoothing down by five,
// from 256-273 to 251-268, and moved the three clocks-event slowdown counters
// and the two power sync balancing fields from 251-255 up to 269-273. The two
// layouts overlap, so the same id names different fields on either side of
// the change, and only the simulated driver's version says which one a caller
// means.
//
// The engine dispatches on the pre-590 numbering throughout; a profile on 590
// or later has its ids translated here first.

// fieldRenumberingDriver is the first driver branch with the NVML 13.1 layout.
const fieldRenumberingDriver = "590.0"

// CanonicalFieldID maps a field id as a caller on driverVersion numbers it to
// the pre-590 numbering the engine dispatches on.
func CanonicalFieldID(fieldID uint32, driverVersion string) uint32 {
	if CompareDriverVersions(driverVersion, fieldRenumberingDriver) < 0 {
		return fieldID
	}
	switch {
	case fieldID >= 251 && fieldID <= 268: // power smoothing
		return fieldID + 5
	case fieldID >= 269 && fieldID <= 273: // slowdown counters, power sync balancing
		return fieldID - 18
	}
	return fieldID
}
