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
	"github.com/stretchr/testify/require"
)

// twoDevices initializes an engine from cfg and returns its first two
// ConfigurableDevices, for pairwise P2P assertions.
func twoDevices(t *testing.T, cfg *Config) (*ConfigurableDevice, *ConfigurableDevice) {
	t.Helper()
	e := NewEngine(cfg)
	require.Equal(t, nvml.SUCCESS, e.Init(), "engine init")
	t.Cleanup(func() { _ = e.Shutdown() })

	h0, _ := e.DeviceGetHandleByIndex(0)
	h1, _ := e.DeviceGetHandleByIndex(1)
	cd0, ok0 := e.LookupDevice(h0).(*ConfigurableDevice)
	cd1, ok1 := e.LookupDevice(h1).(*ConfigurableDevice)
	require.True(t, ok0 && ok1, "expected ConfigurableDevice handles")
	return cd0, cd1
}

// TestGetP2PStatus_SwitchFabric is the unit-level guard for the
// `nvidia-smi topo -m` NV# regression: nvidia-smi renders an NV# cell only
// when nvmlDeviceGetP2PStatus reports the pair NVLink-P2P-OK. Switch-fanned
// GPUs (gb200-like) must therefore report OK between distinct GPUs and OK on
// the diagonal.
func TestGetP2PStatus_SwitchFabric(t *testing.T) {
	yaml := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.35.03", NumDevices: 2},
		NVLink: &NVLinkConfig{
			Version:              5,
			LinksPerGPU:          18,
			BandwidthPerLinkGBPS: 53,
			Switches: []NVSwitchConfig{
				{BDF: "0000:01:00.0"},
				{BDF: "0000:02:00.0"},
			},
		},
	}
	cd0, cd1 := twoDevices(t, &Config{NumDevices: 2, DriverVersion: "560.35.03", YAMLConfig: yaml})

	for _, idx := range []nvml.GpuP2PCapsIndex{nvml.P2P_CAPS_INDEX_NVLINK, nvml.P2P_CAPS_INDEX_READ} {
		status, ret := cd0.GetP2PStatus(cd1, idx)
		require.Equal(t, nvml.SUCCESS, ret, "P2PStatus(0,1,idx=%d) ret", idx)
		require.Equal(t, nvml.P2P_STATUS_OK, status, "P2PStatus(0,1,idx=%d) status", idx)
	}

	// Symmetric.
	status, ret := cd1.GetP2PStatus(cd0, nvml.P2P_CAPS_INDEX_NVLINK)
	require.Equal(t, nvml.SUCCESS, ret, "P2PStatus(1,0) ret")
	require.Equal(t, nvml.P2P_STATUS_OK, status, "P2PStatus(1,0) status")

	// Diagonal: a device is always P2P-OK with itself.
	status, ret = cd0.GetP2PStatus(cd0, nvml.P2P_CAPS_INDEX_NVLINK)
	require.Equal(t, nvml.SUCCESS, ret, "P2PStatus(0,0) ret")
	require.Equal(t, nvml.P2P_STATUS_OK, status, "P2PStatus(0,0) status")
}

// TestGetP2PStatus_NoNVLink verifies a profile without an NVLink fabric
// reports NOT_SUPPORTED between distinct GPUs, so nvidia-smi falls back to
// the PCIe path (no NV# cell) — the negative control (b200/t4/l40s).
func TestGetP2PStatus_NoNVLink(t *testing.T) {
	cfg := &Config{NumDevices: 2, DriverVersion: "550.0", YAMLConfig: &YAMLConfig{
		System: SystemConfig{DriverVersion: "550.0", NumDevices: 2},
	}}
	cd0, cd1 := twoDevices(t, cfg)

	status, ret := cd0.GetP2PStatus(cd1, nvml.P2P_CAPS_INDEX_NVLINK)
	require.Equal(t, nvml.SUCCESS, ret, "P2PStatus(0,1) no-nvlink ret")
	require.Equal(t, nvml.P2P_STATUS_NOT_SUPPORTED, status, "P2PStatus(0,1) no-nvlink status")
}
