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

// SetPowerManagementLimit applies a new power cap, in milliwatts.
//
// The cap is held in memory rather than written back to the profile because
// that is what real NVML does: the limit lives in driver state and is lost on
// driver unload. It outranks the profile's enforced_limit_mw for the same
// reason a cap applied at runtime outranks the board default.
func (d *ConfigurableDevice) SetPowerManagementLimit(limitMW uint32) nvml.Return {
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		debugLog("[NVML] nvmlDeviceSetPowerManagementLimit(%d) -> %d (injected failure)\n", limitMW, ret)
		return ret
	}
	c := d.cfg()
	if c.Power == nil {
		debugLog("[NVML] nvmlDeviceSetPowerManagementLimit(%d) -> NOT_SUPPORTED (no power config)\n", limitMW)
		return nvml.ERROR_NOT_SUPPORTED
	}
	if !powerLimitInRange(c.Power, limitMW) {
		debugLog("[NVML] nvmlDeviceSetPowerManagementLimit(%d) -> INVALID_ARGUMENT (constraints %d-%d mW)\n",
			limitMW, c.Power.MinLimitMW, c.Power.MaxLimitMW)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d.powerLimitOverrideMW.Store(limitMW)
	debugLog("[NVML] nvmlDeviceSetPowerManagementLimit(%d mW)\n", limitMW)
	return nvml.SUCCESS
}

// SetPowerManagementLimit_v2 is the versioned form of the cap setter. Only the
// GPU-wide budget is modelled, so the other scopes decline rather than fold
// into the GPU limit: reporting success for a module or memory cap that was
// never applied is the failure mode this guards against.
func (d *ConfigurableDevice) SetPowerManagementLimit_v2(powerValue *nvml.PowerValue_v2) nvml.Return {
	if powerValue == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if powerValue.PowerScope >= nvml.POWER_SCOPE_COUNT {
		debugLog("[NVML] nvmlDeviceSetPowerManagementLimit_v2 -> INVALID_ARGUMENT (scope %d)\n", powerValue.PowerScope)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if powerValue.PowerScope != nvml.POWER_SCOPE_GPU {
		debugLog("[NVML] nvmlDeviceSetPowerManagementLimit_v2 -> NOT_SUPPORTED (scope %d unmodelled)\n",
			powerValue.PowerScope)
		return nvml.ERROR_NOT_SUPPORTED
	}
	return d.SetPowerManagementLimit(powerValue.PowerValueMw)
}

// powerLimitOverride returns the cap set at runtime, or zero when the profile's
// configured limit still stands.
func (d *ConfigurableDevice) powerLimitOverride() uint32 {
	return d.powerLimitOverrideMW.Load()
}

// powerLimitInRange reports whether a requested cap sits within the constraints
// the device advertises through GetPowerManagementLimitConstraints, which are
// inclusive. A profile may omit those bounds; inventing them would decline caps
// real hardware accepts, so only a zero cap is refused outright.
func powerLimitInRange(p *PowerConfig, limitMW uint32) bool {
	if limitMW == 0 {
		return false
	}
	if p.MinLimitMW > 0 && limitMW < p.MinLimitMW {
		return false
	}
	if p.MaxLimitMW > 0 && limitMW > p.MaxLimitMW {
		return false
	}
	return true
}
