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
// Blackwell board reports when a profile declares none.
//
// The values are opaque driver indices — neither nvml.h nor the NVML API
// reference enumerates them. The set is pinned to five entries because that is
// how many the nvidia-smi the mock image bundles can name
// (0=FULL, 1=OFF, 2=MIN, 3=HALF, 4=3QUARTER); a sixth value would index past
// its name table.
var defaultNvlinkBwModes = []uint8{0, 1, 2, 3, 4}

// supportsNvlinkBwMode gates the device-level bandwidth-mode surface on
// Blackwell, which is what the upstream header specifies ("For Blackwell or
// newer fully supported devices"). UNKNOWN is excluded so an unconfigured
// architecture does not accidentally claim support, matching reportsTLimit.
func (d *ConfigurableDevice) supportsNvlinkBwMode() bool {
	return d.Config.Architecture >= nvml.DEVICE_ARCH_BLACKWELL &&
		d.Config.Architecture != nvml.DEVICE_ARCH_UNKNOWN
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

// nvlinkSupportedBwModes resolves the effective supported list: profile
// override first, architecture default otherwise.
func (d *ConfigurableDevice) nvlinkSupportedBwModes() []uint8 {
	if configured := d.fabric.NvlinkSupportedBwModes(); len(configured) > 0 {
		return configured
	}
	return defaultNvlinkBwModes
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
	if v := d.nvlinkBwModeOverride; v != nil {
		mode = *v
	}

	debugLog("[NVML] nvmlDeviceGetNvlinkBwMode -> mode=%d isBest=%t\n", mode, mode == best)
	return mode, mode == best, nvml.SUCCESS
}

// GetMockNvLinkInfo backs nvmlDeviceGetNvLinkInfo, whose only field the mock
// models is NVLink encryption (NVLE) — the " NVLE:" row of
// `nvidia-smi nvlink --info`.
func (d *ConfigurableDevice) GetMockNvLinkInfo() (bool, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return false, ret
	}
	if !d.supportsNvlinkBwMode() {
		return false, nvml.ERROR_NOT_SUPPORTED
	}
	enabled := d.fabric.NvleEnabled()
	debugLog("[NVML] nvmlDeviceGetNvLinkInfo -> isNvleEnabled=%t\n", enabled)
	return enabled, nvml.SUCCESS
}

// supportsNvlinkLowPower gates the NVLink low-power threshold on Hopper,
// which is what the upstream header specifies ("For Hopper or newer fully
// supported devices").
func (d *ConfigurableDevice) supportsNvlinkLowPower() bool {
	return d.Config.Architecture >= nvml.DEVICE_ARCH_HOPPER &&
		d.Config.Architecture != nvml.DEVICE_ARCH_UNKNOWN
}

// SetMockNvlinkBwMode backs nvmlDeviceSetNvlinkBwMode and
// `nvidia-smi nvlink -sBwMode`. setBest maps onto the struct's bSetBest
// field, which selects the best mode and makes the mode argument irrelevant.
//
// The new mode is held in memory only, so it is not visible to a later
// process — see the nvlinkBwModeOverride comment on ConfigurableDevice.
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

	d.nvlinkBwModeOverride = &mode
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
	if threshold == nvlinkLowPowerThresholdReset {
		d.nvlinkLowPowerOverride = nil
		debugLog("[NVML] nvmlDeviceSetNvLinkDeviceLowPowerThreshold -> reset\n")
		return nvml.SUCCESS
	}
	if threshold < lowPowerThresholdMin || threshold > lowPowerThresholdMax {
		debugLog("[NVML] nvmlDeviceSetNvLinkDeviceLowPowerThreshold(%d) out of range %d..%d\n",
			threshold, lowPowerThresholdMin, lowPowerThresholdMax)
		return nvml.ERROR_INVALID_ARGUMENT
	}

	d.nvlinkLowPowerOverride = &threshold
	debugLog("[NVML] nvmlDeviceSetNvLinkDeviceLowPowerThreshold -> %d\n", threshold)
	return nvml.SUCCESS
}

// systemSupportsNvlinkBwMode gates the global pair on Hopper, which is what
// the upstream docs specify ("NVML_ERROR_NOT_SUPPORTED if GPU is not Hopper or
// newer architecture"). It also returns the architecture the decision was made
// on so a rejection can name it.
//
// The architecture is read from device_defaults rather than a device handle
// because these two APIs take no device parameter, and every profile the repo
// ships is homogeneous.
func (e *Engine) systemSupportsNvlinkBwMode() (nvml.DeviceArchitecture, bool) {
	if e == nil || e.config == nil || e.config.YAMLConfig == nil {
		return nvml.DEVICE_ARCH_UNKNOWN, false
	}
	arch := parseArchitecture(e.config.YAMLConfig.DeviceDefaults.Architecture)
	return arch, arch >= nvml.DEVICE_ARCH_HOPPER && arch != nvml.DEVICE_ARCH_UNKNOWN
}

// SystemGetNvlinkBwMode backs nvmlSystemGetNvlinkBwMode: the node-wide NVLink
// Reduced Bandwidth Mode.
func (e *Engine) SystemGetNvlinkBwMode() (uint32, nvml.Return) {
	if arch, ok := e.systemSupportsNvlinkBwMode(); !ok {
		debugLog("[NVML] nvmlSystemGetNvlinkBwMode unsupported; architecture=%d needs >= hopper\n", arch)
		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	e.systemNvlinkBwModeMu.Lock()
	defer e.systemNvlinkBwModeMu.Unlock()

	mode := uint32(bestNvlinkBwMode(defaultNvlinkBwModes))
	if e.systemNvlinkBwModeOverride != nil {
		mode = *e.systemNvlinkBwModeOverride
	}
	debugLog("[NVML] nvmlSystemGetNvlinkBwMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// SystemSetNvlinkBwMode backs nvmlSystemSetNvlinkBwMode.
//
// The docs also list NVML_ERROR_NO_PERMISSION for a non-root caller and
// NVML_ERROR_IN_USE when a P2P object exists. Neither is simulated: the mock
// models no notion of privilege and has no P2P object lifecycle.
func (e *Engine) SystemSetNvlinkBwMode(mode uint32) nvml.Return {
	if arch, ok := e.systemSupportsNvlinkBwMode(); !ok {
		debugLog("[NVML] nvmlSystemSetNvlinkBwMode(%d) unsupported; architecture=%d needs >= hopper\n",
			mode, arch)
		return nvml.ERROR_NOT_SUPPORTED
	}
	if mode > math.MaxUint8 || !slices.Contains(defaultNvlinkBwModes, uint8(mode)) {
		debugLog("[NVML] nvmlSystemSetNvlinkBwMode(%d) rejected; supported=%v\n",
			mode, defaultNvlinkBwModes)
		return nvml.ERROR_INVALID_ARGUMENT
	}

	e.systemNvlinkBwModeMu.Lock()
	defer e.systemNvlinkBwModeMu.Unlock()

	e.systemNvlinkBwModeOverride = &mode
	debugLog("[NVML] nvmlSystemSetNvlinkBwMode -> %d\n", mode)
	return nvml.SUCCESS
}
