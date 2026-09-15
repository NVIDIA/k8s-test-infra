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
