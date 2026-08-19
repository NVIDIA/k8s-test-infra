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
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
)

func makeVirtualizationDevice(t *testing.T, virt *VirtualizationConfig) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*mockserver.Device)
	return NewConfigurableDevice(0, bd, &DeviceConfig{Virtualization: virt},
		"GPU-00000000-0000-0000-0000-000000000000", "0000:01:00.0", 0, (*NodeFabric)(nil))
}

// Issue #640: every profile ships virtualization.mode but nothing read it, so
// nvidia-smi reported "Virtualization Mode : N/A". N/A is the wrong claim for
// bare metal — real hardware always knows whether it is virtualized, and NONE
// is that answer.
func TestConfigurableDevice_GetVirtualizationModeReportsConfiguredMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want nvml.GpuVirtualizationMode
	}{
		{"none", nvml.GPU_VIRTUALIZATION_MODE_NONE},
		{"passthrough", nvml.GPU_VIRTUALIZATION_MODE_PASSTHROUGH},
		{"vgpu", nvml.GPU_VIRTUALIZATION_MODE_VGPU},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dev := makeVirtualizationDevice(t, &VirtualizationConfig{Mode: tc.mode})

			mode, ret := dev.GetVirtualizationMode()
			require.Equal(t, nvml.SUCCESS, ret, "GetVirtualizationMode")
			require.Equal(t, tc.want, mode, "virtualization.mode %q", tc.mode)
		})
	}
}

// A profile with no virtualization block, or a mode nobody recognises, must
// still answer SUCCESS with NONE. The mock only ever simulates bare-metal
// hardware, so bare metal is the safe default rather than an error.
func TestConfigurableDevice_GetVirtualizationModeDefaultsToBareMetal(t *testing.T) {
	for name, virt := range map[string]*VirtualizationConfig{
		"no virtualization block": nil,
		"empty mode":              {},
		"unrecognised mode":       {Mode: "not-a-mode"},
	} {
		t.Run(name, func(t *testing.T) {
			dev := makeVirtualizationDevice(t, virt)

			mode, ret := dev.GetVirtualizationMode()
			require.Equal(t, nvml.SUCCESS, ret, "GetVirtualizationMode")
			require.Equal(t, nvml.GPU_VIRTUALIZATION_MODE_NONE, mode, name)
		})
	}
}
