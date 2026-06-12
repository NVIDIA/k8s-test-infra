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
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// testdataDir returns the absolute path to the profiles directory.
func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "deployments", "nvml-mock", "helm", "nvml-mock", "profiles")
}

func TestLoadConfig_L40SProfile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "l40s.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	require.NoError(t, err, "Failed to load L40S profile")

	// Verify device count: L40S typically 8 GPUs in a server
	require.Len(t, yamlCfg.Devices, 8, "L40S device count")

	// Verify architecture
	require.Equal(t, "ada_lovelace", yamlCfg.DeviceDefaults.Architecture, "L40S architecture")

	// Verify compute capability 8.9
	cc := yamlCfg.DeviceDefaults.ComputeCapability
	require.NotNil(t, cc, "L40S compute_capability is nil")
	require.Equal(t, 8, cc.Major, "L40S compute capability major")
	require.Equal(t, 9, cc.Minor, "L40S compute capability minor")

	// Verify memory: 48 GiB = 51539607552 bytes
	mem := yamlCfg.DeviceDefaults.Memory
	require.NotNil(t, mem, "L40S memory config is nil")
	expectedMemBytes := uint64(51539607552) // 48 GiB
	require.Equal(t, expectedMemBytes, mem.TotalBytes, "L40S memory total_bytes")

	// Verify PCI device ID: 0x26B510DE
	pci := yamlCfg.DeviceDefaults.PCI
	require.NotNil(t, pci, "L40S PCI config is nil")
	expectedDeviceID := uint32(0x26B510DE)
	require.Equal(t, expectedDeviceID, pci.DeviceID, "L40S PCI device_id")

	// Verify GPU name
	require.Equal(t, "NVIDIA L40S", yamlCfg.DeviceDefaults.Name, "L40S name")

	// Verify no NVLink (L40S is PCIe only)
	require.Nil(t, yamlCfg.NVLink, "L40S should not have NVLink configuration")

	// Verify PCIe Gen4
	pcie := yamlCfg.DeviceDefaults.PCIe
	require.NotNil(t, pcie, "L40S PCIe config is nil")
	require.Equal(t, 4, pcie.MaxLinkGen, "L40S PCIe max_link_gen")

	// Verify power: 350W TDP
	power := yamlCfg.DeviceDefaults.Power
	require.NotNil(t, power, "L40S power config is nil")
	require.Equal(t, uint32(350000), power.DefaultLimitMW, "L40S power default_limit_mw")
}

func TestLoadConfig_T4Profile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "t4.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	require.NoError(t, err, "Failed to load T4 profile")

	// Verify device count: T4 typically 4 GPUs
	require.Len(t, yamlCfg.Devices, 4, "T4 device count")

	// Verify architecture
	require.Equal(t, "turing", yamlCfg.DeviceDefaults.Architecture, "T4 architecture")

	// Verify compute capability 7.5
	cc := yamlCfg.DeviceDefaults.ComputeCapability
	require.NotNil(t, cc, "T4 compute_capability is nil")
	require.Equal(t, 7, cc.Major, "T4 compute capability major")
	require.Equal(t, 5, cc.Minor, "T4 compute capability minor")

	// Verify memory: 16 GiB = 17179869184 bytes
	mem := yamlCfg.DeviceDefaults.Memory
	require.NotNil(t, mem, "T4 memory config is nil")
	expectedMemBytes := uint64(17179869184) // 16 GiB
	require.Equal(t, expectedMemBytes, mem.TotalBytes, "T4 memory total_bytes")

	// Verify PCI device ID: 0x1EB810DE
	pci := yamlCfg.DeviceDefaults.PCI
	require.NotNil(t, pci, "T4 PCI config is nil")
	expectedDeviceID := uint32(0x1EB810DE)
	require.Equal(t, expectedDeviceID, pci.DeviceID, "T4 PCI device_id")

	// Verify GPU name
	require.Equal(t, "NVIDIA T4", yamlCfg.DeviceDefaults.Name, "T4 name")

	// Verify no NVLink (T4 is PCIe only)
	require.Nil(t, yamlCfg.NVLink, "T4 should not have NVLink configuration")

	// Verify PCIe Gen3
	pcie := yamlCfg.DeviceDefaults.PCIe
	require.NotNil(t, pcie, "T4 PCIe config is nil")
	require.Equal(t, 3, pcie.MaxLinkGen, "T4 PCIe max_link_gen")

	// Verify power: 70W TDP
	power := yamlCfg.DeviceDefaults.Power
	require.NotNil(t, power, "T4 power config is nil")
	require.Equal(t, uint32(70000), power.DefaultLimitMW, "T4 power default_limit_mw")
}

func TestLoadConfig_GB300Profile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "gb300.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	require.NoError(t, err, "Failed to load GB300 profile")

	// 8 GPUs: 4 Grace-Blackwell Ultra superchips × 2 B300 GPUs each.
	require.Len(t, yamlCfg.Devices, 8, "GB300 device count")

	require.Equal(t, "NVIDIA GB300 NVL", yamlCfg.DeviceDefaults.Name, "GB300 name")

	// 288 GiB HBM3e per GPU is the headline GB300 vs. GB200 delta — make
	// sure a regression in the YAML can never quietly drop us back to 192.
	mem := yamlCfg.DeviceDefaults.Memory
	require.NotNil(t, mem, "GB300 memory config is nil")
	expectedMemBytes := uint64(288) * 1024 * 1024 * 1024
	require.Equal(t, expectedMemBytes, mem.TotalBytes, "GB300 memory total_bytes (288 GiB)")

	// Blackwell Ultra uses the 570.x driver line; the chart's
	// driverVersion helper relies on this value being consistent.
	require.Equal(t, "570.124.06", yamlCfg.System.DriverVersion, "GB300 driver_version")

	// PCIe Gen6 (or NVLink-C2C to Grace).
	pcie := yamlCfg.DeviceDefaults.PCIe
	require.NotNil(t, pcie, "GB300 PCIe config is nil")
	require.Equal(t, 6, pcie.MaxLinkGen, "GB300 PCIe max_link_gen")

	// 1400W default TDP (vs. GB200's 1000W).
	power := yamlCfg.DeviceDefaults.Power
	require.NotNil(t, power, "GB300 power config is nil")
	require.Equal(t, uint32(1400000), power.DefaultLimitMW, "GB300 power default_limit_mw")

	// Grace pairing must be wired up — GB300 is a superchip part.
	grace := yamlCfg.DeviceDefaults.GraceSuperchip
	require.NotNil(t, grace, "GB300 grace_superchip config is nil")
	require.True(t, grace.Enabled, "GB300 grace_superchip.enabled: got false, want true")

	// NVLink v5, 18 links @ 100 GB/s (same fabric as GB200).
	require.NotNil(t, yamlCfg.NVLink, "GB300 NVLink config is nil")
	require.Equal(t, 5, yamlCfg.NVLink.Version, "GB300 nvlink.version")
	require.Equal(t, 18, yamlCfg.NVLink.LinksPerGPU, "GB300 nvlink.links_per_gpu")
}

func TestLoadConfig_AllProfilesConsistent(t *testing.T) {
	profiles := []struct {
		name         string
		file         string
		architecture string
		ccMajor      int
		ccMinor      int
		memGiB       uint64
		deviceCount  int
	}{
		{"A100", "a100.yaml", "ampere", 8, 0, 40, 8},
		{"H100", "h100.yaml", "hopper", 9, 0, 80, 8},
		{"B200", "b200.yaml", "blackwell", 10, 0, 192, 8},
		{"GB200", "gb200.yaml", "blackwell", 10, 0, 192, 8},
		{"GB300", "gb300.yaml", "blackwell", 10, 0, 288, 8},
		{"L40S", "l40s.yaml", "ada_lovelace", 8, 9, 48, 8},
		{"T4", "t4.yaml", "turing", 7, 5, 16, 4},
	}

	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			profilePath := filepath.Join(testdataDir(), p.file)
			yamlCfg, err := LoadYAMLConfig(profilePath)
			require.NoError(t, err, "Failed to load %s profile", p.name)

			require.Equal(t, p.architecture, yamlCfg.DeviceDefaults.Architecture, "%s architecture", p.name)

			cc := yamlCfg.DeviceDefaults.ComputeCapability
			require.NotNil(t, cc, "%s compute_capability is nil", p.name)
			require.Equal(t, p.ccMajor, cc.Major, "%s compute capability major", p.name)
			require.Equal(t, p.ccMinor, cc.Minor, "%s compute capability minor", p.name)

			mem := yamlCfg.DeviceDefaults.Memory
			require.NotNil(t, mem, "%s memory config is nil", p.name)
			expectedBytes := p.memGiB * 1024 * 1024 * 1024
			require.Equal(t, expectedBytes, mem.TotalBytes, "%s memory (%d GiB)", p.name, p.memGiB)

			require.Len(t, yamlCfg.Devices, p.deviceCount, "%s device count", p.name)

			require.NotEmpty(t, yamlCfg.System.DriverVersion, "%s driver_version is empty", p.name)
		})
	}
}
