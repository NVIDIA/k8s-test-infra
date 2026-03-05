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
)

// testdataDir returns the absolute path to the profiles directory.
func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "deployments", "gpu-mock", "helm", "gpu-mock", "profiles")
}

func TestLoadConfig_L40SProfile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "l40s.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	if err != nil {
		t.Fatalf("Failed to load L40S profile: %v", err)
	}

	// Verify device count: L40S typically 8 GPUs in a server
	if got := len(yamlCfg.Devices); got != 8 {
		t.Errorf("L40S device count: got %d, want 8", got)
	}

	// Verify architecture
	if got := yamlCfg.DeviceDefaults.Architecture; got != "ada_lovelace" {
		t.Errorf("L40S architecture: got %q, want %q", got, "ada_lovelace")
	}

	// Verify compute capability 8.9
	cc := yamlCfg.DeviceDefaults.ComputeCapability
	if cc == nil {
		t.Fatal("L40S compute_capability is nil")
	}
	if cc.Major != 8 || cc.Minor != 9 {
		t.Errorf("L40S compute capability: got %d.%d, want 8.9", cc.Major, cc.Minor)
	}

	// Verify memory: 48 GiB = 51539607552 bytes
	mem := yamlCfg.DeviceDefaults.Memory
	if mem == nil {
		t.Fatal("L40S memory config is nil")
	}
	expectedMemBytes := uint64(51539607552) // 48 GiB
	if mem.TotalBytes != expectedMemBytes {
		t.Errorf("L40S memory total_bytes: got %d, want %d", mem.TotalBytes, expectedMemBytes)
	}

	// Verify PCI device ID: 0x26B510DE
	pci := yamlCfg.DeviceDefaults.PCI
	if pci == nil {
		t.Fatal("L40S PCI config is nil")
	}
	expectedDeviceID := uint32(0x26B510DE)
	if pci.DeviceID != expectedDeviceID {
		t.Errorf("L40S PCI device_id: got 0x%08X, want 0x%08X", pci.DeviceID, expectedDeviceID)
	}

	// Verify GPU name
	if got := yamlCfg.DeviceDefaults.Name; got != "NVIDIA L40S" {
		t.Errorf("L40S name: got %q, want %q", got, "NVIDIA L40S")
	}

	// Verify no NVLink (L40S is PCIe only)
	if yamlCfg.NVLink != nil {
		t.Error("L40S should not have NVLink configuration")
	}

	// Verify PCIe Gen4
	pcie := yamlCfg.DeviceDefaults.PCIe
	if pcie == nil {
		t.Fatal("L40S PCIe config is nil")
	}
	if pcie.MaxLinkGen != 4 {
		t.Errorf("L40S PCIe max_link_gen: got %d, want 4", pcie.MaxLinkGen)
	}

	// Verify power: 350W TDP
	power := yamlCfg.DeviceDefaults.Power
	if power == nil {
		t.Fatal("L40S power config is nil")
	}
	if power.DefaultLimitMW != 350000 {
		t.Errorf("L40S power default_limit_mw: got %d, want 350000", power.DefaultLimitMW)
	}
}

func TestLoadConfig_T4Profile(t *testing.T) {
	profilePath := filepath.Join(testdataDir(), "t4.yaml")

	yamlCfg, err := LoadYAMLConfig(profilePath)
	if err != nil {
		t.Fatalf("Failed to load T4 profile: %v", err)
	}

	// Verify device count: T4 typically 4 GPUs
	if got := len(yamlCfg.Devices); got != 4 {
		t.Errorf("T4 device count: got %d, want 4", got)
	}

	// Verify architecture
	if got := yamlCfg.DeviceDefaults.Architecture; got != "turing" {
		t.Errorf("T4 architecture: got %q, want %q", got, "turing")
	}

	// Verify compute capability 7.5
	cc := yamlCfg.DeviceDefaults.ComputeCapability
	if cc == nil {
		t.Fatal("T4 compute_capability is nil")
	}
	if cc.Major != 7 || cc.Minor != 5 {
		t.Errorf("T4 compute capability: got %d.%d, want 7.5", cc.Major, cc.Minor)
	}

	// Verify memory: 16 GiB = 17179869184 bytes
	mem := yamlCfg.DeviceDefaults.Memory
	if mem == nil {
		t.Fatal("T4 memory config is nil")
	}
	expectedMemBytes := uint64(17179869184) // 16 GiB
	if mem.TotalBytes != expectedMemBytes {
		t.Errorf("T4 memory total_bytes: got %d, want %d", mem.TotalBytes, expectedMemBytes)
	}

	// Verify PCI device ID: 0x1EB810DE
	pci := yamlCfg.DeviceDefaults.PCI
	if pci == nil {
		t.Fatal("T4 PCI config is nil")
	}
	expectedDeviceID := uint32(0x1EB810DE)
	if pci.DeviceID != expectedDeviceID {
		t.Errorf("T4 PCI device_id: got 0x%08X, want 0x%08X", pci.DeviceID, expectedDeviceID)
	}

	// Verify GPU name
	if got := yamlCfg.DeviceDefaults.Name; got != "NVIDIA T4" {
		t.Errorf("T4 name: got %q, want %q", got, "NVIDIA T4")
	}

	// Verify no NVLink (T4 is PCIe only)
	if yamlCfg.NVLink != nil {
		t.Error("T4 should not have NVLink configuration")
	}

	// Verify PCIe Gen3
	pcie := yamlCfg.DeviceDefaults.PCIe
	if pcie == nil {
		t.Fatal("T4 PCIe config is nil")
	}
	if pcie.MaxLinkGen != 3 {
		t.Errorf("T4 PCIe max_link_gen: got %d, want 3", pcie.MaxLinkGen)
	}

	// Verify power: 70W TDP
	power := yamlCfg.DeviceDefaults.Power
	if power == nil {
		t.Fatal("T4 power config is nil")
	}
	if power.DefaultLimitMW != 70000 {
		t.Errorf("T4 power default_limit_mw: got %d, want 70000", power.DefaultLimitMW)
	}
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
		{"L40S", "l40s.yaml", "ada_lovelace", 8, 9, 48, 8},
		{"T4", "t4.yaml", "turing", 7, 5, 16, 4},
	}

	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			profilePath := filepath.Join(testdataDir(), p.file)
			yamlCfg, err := LoadYAMLConfig(profilePath)
			if err != nil {
				t.Fatalf("Failed to load %s profile: %v", p.name, err)
			}

			if yamlCfg.DeviceDefaults.Architecture != p.architecture {
				t.Errorf("%s architecture: got %q, want %q", p.name, yamlCfg.DeviceDefaults.Architecture, p.architecture)
			}

			cc := yamlCfg.DeviceDefaults.ComputeCapability
			if cc == nil {
				t.Fatalf("%s compute_capability is nil", p.name)
			}
			if cc.Major != p.ccMajor || cc.Minor != p.ccMinor {
				t.Errorf("%s compute capability: got %d.%d, want %d.%d", p.name, cc.Major, cc.Minor, p.ccMajor, p.ccMinor)
			}

			mem := yamlCfg.DeviceDefaults.Memory
			if mem == nil {
				t.Fatalf("%s memory config is nil", p.name)
			}
			expectedBytes := p.memGiB * 1024 * 1024 * 1024
			if mem.TotalBytes != expectedBytes {
				t.Errorf("%s memory: got %d bytes, want %d bytes (%d GiB)", p.name, mem.TotalBytes, expectedBytes, p.memGiB)
			}

			if len(yamlCfg.Devices) != p.deviceCount {
				t.Errorf("%s device count: got %d, want %d", p.name, len(yamlCfg.Devices), p.deviceCount)
			}

			if yamlCfg.System.DriverVersion == "" {
				t.Errorf("%s driver_version is empty", p.name)
			}
		})
	}
}
