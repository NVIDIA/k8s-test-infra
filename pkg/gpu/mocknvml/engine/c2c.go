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

// GetMockC2cMode reports whether this GPU has an NVLink-C2C link to the host
// CPU, backing nvmlDeviceGetC2cModeInfoV and the "GPU C2C Mode" row of
// `nvidia-smi -q`.
//
// A disabled or undeclared link yields ERROR_NOT_SUPPORTED, which nvidia-smi
// renders as N/A — the correct reading on every non-Grace board. Returning
// SUCCESS with a false reading would render as "Disabled" instead, which no
// shipped profile wants: a100, h100, and b200 all set c2c_enabled: false
// explicitly and must stay N/A.
//
// Named GetMockC2cMode to avoid shadowing the embedded dgxa100.Device's
// GetC2cModeInfoV method, following GetMockFabricInfo.
func (d *ConfigurableDevice) GetMockC2cMode() (bool, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return false, ret
	}
	if !d.fabric.C2CEnabled() {
		return false, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlDeviceGetC2cModeInfoV -> isC2cEnabled=1\n")
	return true, nvml.SUCCESS
}
