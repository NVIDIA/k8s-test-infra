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

func makeC2CDevice(t *testing.T, fabric *NodeFabric) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*mockserver.Device)
	return NewConfigurableDevice(0, bd, &DeviceConfig{},
		"GPU-00000000-0000-0000-0000-000000000000", "0000:01:00.0", 0, fabric)
}

func c2cFabric(t *testing.T, nvlink *NVLinkConfig) *NodeFabric {
	t.Helper()
	return BuildNodeFabric(&Config{
		NumDevices: 1,
		YAMLConfig: &YAMLConfig{NVLink: nvlink},
	})
}

func TestGetMockC2cMode_EnabledWhenProfileDeclaresC2C(t *testing.T) {
	dev := makeC2CDevice(t, c2cFabric(t, &NVLinkConfig{C2CEnabled: true}))
	enabled, ret := dev.GetMockC2cMode()
	require.Equal(t, nvml.SUCCESS, ret, "return code")
	require.True(t, enabled, "isC2cEnabled")
}

// TestGetMockC2cMode_NotSupported pins the negative direction, which is what
// keeps the fix from degenerating into a hardcoded "Enabled". An explicitly
// disabled profile must be indistinguishable from one with no nvlink block:
// both render N/A, never "Disabled".
func TestGetMockC2cMode_NotSupported(t *testing.T) {
	cases := map[string]*NodeFabric{
		"explicit false":  c2cFabric(t, &NVLinkConfig{C2CEnabled: false}),
		"no nvlink block": c2cFabric(t, nil),
		"nil fabric":      (*NodeFabric)(nil),
	}
	for name, fabric := range cases {
		t.Run(name, func(t *testing.T) {
			dev := makeC2CDevice(t, fabric)
			enabled, ret := dev.GetMockC2cMode()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "return code")
			require.False(t, enabled, "isC2cEnabled")
		})
	}
}
