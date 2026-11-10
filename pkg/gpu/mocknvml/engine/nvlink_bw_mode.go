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
	"math"
	"slices"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// defaultNvlinkBwModes is the supported NVLink Reduced Bandwidth Mode list a
// board reports when its profile's bw_mode block lists none.
//
// The `nvidia-smi` manual documents the five mode names and what each does
// (FULL, OFF, MIN, HALF, 3QUARTER), but nothing states which index the NVML
// calls take for a name, so the mapping below (0=FULL, 1=OFF, 2=MIN, 3=HALF,
// 4=3QUARTER) was established against the nvidia-smi the mock image bundles.
// The set stops at five because a sixth would index past its name table.
var defaultNvlinkBwModes = []uint8{0, 1, 2, 3, 4}

// maxNameableNvlinkBwMode is the highest index the bundled nvidia-smi has a
// name for. A configured value above it is legal — the indices are the driver's
// and a future one may define more — but it renders as an unnamed mode, so the
// fabric warns about it.
const maxNameableNvlinkBwMode = 4

// effectiveNvlinkBwModes is the supported list a board answers with: the
// profile's own when it declares one, the default five otherwise. Named
// once so config validation and the device getters cannot disagree about which
// modes are in play.
func effectiveNvlinkBwModes(configured []uint8) []uint8 {
	if len(configured) > 0 {
		return configured
	}
	return defaultNvlinkBwModes
}

// supportsNvlinkBwMode gates the per-device bandwidth-mode trio on the profile
// selecting it (nvlink.bw_mode.scope: device).
func (d *ConfigurableDevice) supportsNvlinkBwMode() bool {
	return d.fabric.NvlinkBwModeScope() == NVLinkBwModeScopeDevice
}

// supportsNvlinkInfo gates nvmlDeviceGetNvLinkInfo on the board having NVLink
// at all, rather than on an architecture. It is the one call in this file the
// upstream header states no architecture requirement for, and a Hopper H100
// does answer it — with NVLE off and no firmware table, the state every board
// below Blackwell reports. What a board without NVLink cannot do is answer at
// all, which is the header's "device does not support this feature".
func (d *ConfigurableDevice) supportsNvlinkInfo() bool {
	return d.fabric.HasNvlink(d.index)
}

// bestNvlinkBwMode picks the "best" (highest bandwidth) mode from a supported
// list. Because the values are opaque, lowest-wins is a mock convention rather
// than a driver fact; it is chosen so the default mode 0 (FULL) reads as best,
// which is the semantically right answer for the one value whose meaning the
// bundled nvidia-smi does tell us.
func bestNvlinkBwMode(supported []uint8) uint8 {
	if len(supported) == 0 {
		return 0
	}
	best := supported[0]
	for _, m := range supported[1:] {
		if m < best {
			best = m
		}
	}
	return best
}

// nvlinkSupportedBwModes resolves the effective supported list: the profile's
// first, the default five otherwise.
func (d *ConfigurableDevice) nvlinkSupportedBwModes() []uint8 {
	return effectiveNvlinkBwModes(d.fabric.NvlinkSupportedBwModes())
}

// GetMockNvlinkSupportedBwModes backs nvmlDeviceGetNvlinkSupportedBwModes and
// `nvidia-smi nvlink -sBwMode values`.
//
// Named GetMock* to avoid shadowing the embedded mock Device's
// GetNvlinkSupportedBwModes, following GetMockC2cMode.
func (d *ConfigurableDevice) GetMockNvlinkSupportedBwModes() ([]uint8, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	if !d.supportsNvlinkBwMode() {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	modes := d.nvlinkSupportedBwModes()
	debugLog("[NVML] nvmlDeviceGetNvlinkSupportedBwModes -> %v\n", modes)
	return modes, nvml.SUCCESS
}

// GetMockNvlinkBwMode backs nvmlDeviceGetNvlinkBwMode and
// `nvidia-smi nvlink -gBwMode`. isBest maps onto the struct's bIsBest field.
func (d *ConfigurableDevice) GetMockNvlinkBwMode() (uint8, bool, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, false, ret
	}
	if !d.supportsNvlinkBwMode() {
		return 0, false, nvml.ERROR_NOT_SUPPORTED
	}
	supported := d.nvlinkSupportedBwModes()
	best := bestNvlinkBwMode(supported)

	mode := best
	if configured, ok := d.fabric.NvlinkConfiguredBwMode(); ok {
		mode = configured
	}
	// A mode recorded by a setter outranks the profile, and is read fresh from
	// the override document so a write from another process is visible here.
	if v := d.cfg().NVLinkBwMode; v != nil && honoursRecordedNvlinkBwMode("nvmlDeviceGetNvlinkBwMode", *v, supported) {
		mode = *v
	}

	debugLog("[NVML] nvmlDeviceGetNvlinkBwMode -> mode=%d isBest=%t\n", mode, mode == best)
	return mode, mode == best, nvml.SUCCESS
}

// NvLinkInfo is what nvmlDeviceGetNvLinkInfo reports: NVLink encryption and
// the firmware table, the two halves of `nvidia-smi nvlink --info`.
type NvLinkInfo struct {
	NvleEnabled bool
	Firmware    []NvlinkFirmwareVersion
}

// GetMockNvLinkInfo backs nvmlDeviceGetNvLinkInfo, which feeds the " NVLE:"
// row of `nvidia-smi nvlink --info` and the "Firmware Version:" rows beneath
// it. A profile that declares no firmware reports an empty table, which
// nvidia-smi renders as "N/A".
func (d *ConfigurableDevice) GetMockNvLinkInfo() (NvLinkInfo, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return NvLinkInfo{}, ret
	}
	if !d.supportsNvlinkInfo() {
		return NvLinkInfo{}, nvml.ERROR_NOT_SUPPORTED
	}
	info := NvLinkInfo{
		NvleEnabled: d.fabric.NvleEnabled(),
		Firmware:    d.fabric.NvlinkFirmware(),
	}
	debugLog("[NVML] nvmlDeviceGetNvLinkInfo -> isNvleEnabled=%t firmwareEntries=%d\n",
		info.NvleEnabled, len(info.Firmware))
	return info, nvml.SUCCESS
}

// supportsNvlinkLowPower gates the low-power threshold setter on the same
// condition as its read side: the -gLowPwrInfo field values report the
// threshold supported only on a GPU with an active link, and nvidia-smi
// validates a write against that advertised range, so the two must agree.
func (d *ConfigurableDevice) supportsNvlinkLowPower() bool {
	return d.fabric != nil && d.fabric.ActiveLinkCount(d.index) > 0
}

// SetMockNvlinkBwMode backs nvmlDeviceSetNvlinkBwMode and
// `nvidia-smi nvlink -sBwMode`. setBest maps onto the struct's bSetBest
// field, which selects the best mode and makes the mode argument irrelevant.
//
// The mode is recorded in the override document rather than in process memory,
// because on real hardware it is driver state the whole node observes: setting
// it with one `nvidia-smi` and reading it back with another has to report the
// new value.
func (d *ConfigurableDevice) SetMockNvlinkBwMode(mode uint8, setBest bool) nvml.Return {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return ret
	}
	if !d.supportsNvlinkBwMode() {
		return nvml.ERROR_NOT_SUPPORTED
	}
	supported := d.nvlinkSupportedBwModes()

	if setBest {
		mode = bestNvlinkBwMode(supported)
	} else if !slices.Contains(supported, mode) {
		debugLog("[NVML] nvmlDeviceSetNvlinkBwMode(%d) rejected; supported=%v\n", mode, supported)
		return nvml.ERROR_INVALID_ARGUMENT
	}

	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] nvmlDeviceSetNvlinkBwMode(%d) -> NO_PERMISSION (no override writer)\n", mode)
		return nvml.ERROR_NO_PERMISSION
	}
	if err := w.SetNvlinkBwMode(d.PhysicalIndex(), mode, false); err != nil {
		warnLog("[NVML] nvmlDeviceSetNvlinkBwMode(%d) -> NO_PERMISSION: %v\n", mode, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] nvmlDeviceSetNvlinkBwMode -> mode=%d\n", mode)
	return nvml.SUCCESS
}

// SetMockNvLinkLowPowerThreshold backs
// nvmlDeviceSetNvLinkDeviceLowPowerThreshold and
// `nvidia-smi nvlink -sLowPwrThres`. An accepted value is read back through
// NVML_FI_DEV_NVLINK_GET_POWER_THRESHOLD.
//
// The range is the one the mock already advertises through the
// _THRESHOLD_MIN / _MAX field values, not the header's deprecated 0x1FFF
// ceiling; nvidia-smi reads that advertised range and pre-validates against
// it, so the two must agree.
func (d *ConfigurableDevice) SetMockNvLinkLowPowerThreshold(threshold uint32) nvml.Return {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return ret
	}
	if !d.supportsNvlinkLowPower() {
		return nvml.ERROR_NOT_SUPPORTED
	}
	// The reset sentinel clears the recorded value rather than storing one, so
	// the device returns to the driver default the profile describes.
	var record *uint32
	if threshold != nvlinkLowPowerThresholdReset {
		if threshold < lowPowerThresholdMin || threshold > lowPowerThresholdMax {
			debugLog("[NVML] nvmlDeviceSetNvLinkDeviceLowPowerThreshold(%d) out of range %d..%d\n",
				threshold, lowPowerThresholdMin, lowPowerThresholdMax)
			return nvml.ERROR_INVALID_ARGUMENT
		}
		record = &threshold
	}

	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] nvmlDeviceSetNvLinkDeviceLowPowerThreshold(%d) -> NO_PERMISSION (no override writer)\n",
			threshold)
		return nvml.ERROR_NO_PERMISSION
	}
	if err := w.SetNvlinkLowPowerThreshold(d.PhysicalIndex(), record); err != nil {
		warnLog("[NVML] nvmlDeviceSetNvLinkDeviceLowPowerThreshold(%d) -> NO_PERMISSION: %v\n", threshold, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] nvmlDeviceSetNvLinkDeviceLowPowerThreshold -> %d\n", threshold)
	return nvml.SUCCESS
}

// systemNvlinkBwMode resolves the profile's bw_mode block for the node-wide
// pair and reports whether the profile selects it (nvlink.bw_mode.scope:
// system). The block is read from the config rather than a device's fabric
// because these two APIs take no device parameter.
func (e *Engine) systemNvlinkBwMode() (nvlinkBwModeSpec, bool) {
	if e == nil {
		return nvlinkBwModeSpec{}, false
	}
	// Any warning was already logged when the fabric was built from this
	// same config; repeating it on every call would only add noise.
	spec, _ := resolveNvlinkBwMode(e.config)
	return spec, spec.scope == NVLinkBwModeScopeSystem
}

// SystemGetNvlinkBwMode backs nvmlSystemGetNvlinkBwMode: the node-wide NVLink
// Reduced Bandwidth Mode.
func (e *Engine) SystemGetNvlinkBwMode() (uint32, nvml.Return) {
	spec, ok := e.systemNvlinkBwMode()
	if !ok {
		debugLog("[NVML] nvmlSystemGetNvlinkBwMode unsupported; nvlink.bw_mode.scope=%q is not %q\n",
			spec.scope, NVLinkBwModeScopeSystem)
		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	supported := effectiveNvlinkBwModes(spec.supported)
	mode := uint32(bestNvlinkBwMode(supported))
	if spec.mode != nil {
		mode = uint32(*spec.mode)
	}
	if v, ok := nodeWideNvlinkBwMode(); ok && honoursRecordedNvlinkBwMode("nvmlSystemGetNvlinkBwMode", v, supported) {
		mode = uint32(v)
	}
	debugLog("[NVML] nvmlSystemGetNvlinkBwMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// honoursRecordedNvlinkBwMode reports whether a mode a setter recorded still
// applies. The override document outlives the profile it was written against,
// so after the profile's supported list shrinks the recorded mode may be one
// the setter now refuses; reporting it would have the getter and setter
// disagree about one value. Such a mode is ignored, the same way a profile's
// own out-of-list initial mode is.
func honoursRecordedNvlinkBwMode(api string, recorded uint8, supported []uint8) bool {
	if slices.Contains(supported, recorded) {
		return true
	}
	warnLog("[NVML] %s: recorded mode %d is not in the supported list %v; ignoring it\n",
		api, recorded, supported)
	return false
}

// nodeWideNvlinkBwMode reads the mode the node-wide setter recorded.
//
// It reads the `all:` bucket directly instead of going through a device,
// because this API takes none and a per-device write must not be mistaken for a
// node-wide one: the two NVML pairs are independent, and only the node-wide
// setter writes here.
func nodeWideNvlinkBwMode() (uint8, bool) {
	_, doc := configOverrides.snapshot()
	if doc == nil || len(doc.All) == 0 {
		return 0, false
	}
	merged, err := MergeDeviceConfig(&DeviceConfig{}, doc.All)
	if err != nil || merged.NVLinkBwMode == nil {
		return 0, false
	}
	return *merged.NVLinkBwMode, true
}

// SystemSetNvlinkBwMode backs nvmlSystemSetNvlinkBwMode.
//
// The docs also list NVML_ERROR_NO_PERMISSION for a non-root caller and
// NVML_ERROR_IN_USE when a P2P object exists. Neither is simulated: the mock
// models no notion of privilege and has no P2P object lifecycle.
func (e *Engine) SystemSetNvlinkBwMode(mode uint32) nvml.Return {
	spec, ok := e.systemNvlinkBwMode()
	if !ok {
		debugLog("[NVML] nvmlSystemSetNvlinkBwMode(%d) unsupported; nvlink.bw_mode.scope=%q is not %q\n",
			mode, spec.scope, NVLinkBwModeScopeSystem)
		return nvml.ERROR_NOT_SUPPORTED
	}
	supported := effectiveNvlinkBwModes(spec.supported)
	if mode > math.MaxUint8 || !slices.Contains(supported, uint8(mode)) {
		debugLog("[NVML] nvmlSystemSetNvlinkBwMode(%d) rejected; supported=%v\n", mode, supported)
		return nvml.ERROR_INVALID_ARGUMENT
	}

	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] nvmlSystemSetNvlinkBwMode(%d) -> NO_PERMISSION (no override writer)\n", mode)
		return nvml.ERROR_NO_PERMISSION
	}
	// The index is irrelevant for an `all:` write, but the port takes one so
	// the per-device setter can share the method.
	if err := w.SetNvlinkBwMode(0, uint8(mode), true); err != nil {
		warnLog("[NVML] nvmlSystemSetNvlinkBwMode(%d) -> NO_PERMISSION: %v\n", mode, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] nvmlSystemSetNvlinkBwMode -> %d\n", mode)
	return nvml.SUCCESS
}
