// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	if config == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", config.NumDevices)
	}
	if config.DriverVersion != "550.163.01" {
		t.Errorf("Expected default DriverVersion 550.163.01, got %s", config.DriverVersion)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear config cache to ensure clean state
	ClearConfigCache()

	config := LoadConfig()
	if config == nil {
		t.Fatal("LoadConfig returned nil")
	}
	if config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", config.NumDevices)
	}
	if config.DriverVersion != "550.163.01" {
		t.Errorf("Expected default DriverVersion, got %s", config.DriverVersion)
	}
}

func TestLoadConfig_NumDevices(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
		expected int
	}{
		{"Valid number", "4", true, 4},
		{"Zero devices", "0", true, 0},
		{"Max devices", "8", true, 8},
		{"Invalid string", "abc", true, 8}, // Should use default
		{"Negative number", "-1", true, 8}, // Should use default
		{"Empty string", "", false, 8},     // Should use default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear config cache to ensure env vars take effect
			ClearConfigCache()

			if tt.setEnv {
				t.Setenv("MOCK_NVML_NUM_DEVICES", tt.envValue)
			}

			config := LoadConfig()
			if config.NumDevices != tt.expected {
				t.Errorf("Expected NumDevices %d, got %d", tt.expected, config.NumDevices)
			}
		})
	}
}

func TestLoadConfig_DriverVersion(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	customVersion := "999.99.99"
	t.Setenv("MOCK_NVML_DRIVER_VERSION", customVersion)

	config := LoadConfig()
	if config.DriverVersion != customVersion {
		t.Errorf("Expected DriverVersion %s, got %s", customVersion, config.DriverVersion)
	}
}

func TestLoadConfig_AllEnvVars(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "6")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "600.00.00")

	config := LoadConfig()
	if config.NumDevices != 6 {
		t.Errorf("NumDevices not set correctly: %d", config.NumDevices)
	}
	if config.DriverVersion != "600.00.00" {
		t.Errorf("DriverVersion not set correctly: %s", config.DriverVersion)
	}
}

func TestLoadConfig_EmptyEnvVars(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "")

	config := LoadConfig()
	// Empty strings should result in defaults
	if config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", config.NumDevices)
	}
	if config.DriverVersion != "550.163.01" {
		t.Errorf("Expected default DriverVersion, got %s", config.DriverVersion)
	}
}

func TestLoadConfig_YAMLNumDevices(t *testing.T) {
	// Create a temp config YAML with system.num_devices set
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Config has 2 devices listed but system.num_devices=4
	yamlContent := `version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 4
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
devices:
  - index: 0
    uuid: "GPU-aaaa"
  - index: 1
    uuid: "GPU-bbbb"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	ClearConfigCache()
	t.Setenv("MOCK_NVML_CONFIG", configPath)

	config := LoadConfig()
	if config.NumDevices != 4 {
		t.Errorf("Expected NumDevices=4 from system.num_devices, got %d", config.NumDevices)
	}
}

func TestLoadConfig_YAMLNumDevicesZero(t *testing.T) {
	// When system.num_devices is 0 (or unset), fall back to device list count
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `version: "1.0"
system:
  driver_version: "550.163.01"
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
devices:
  - index: 0
    uuid: "GPU-aaaa"
  - index: 1
    uuid: "GPU-bbbb"
  - index: 2
    uuid: "GPU-cccc"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	ClearConfigCache()
	t.Setenv("MOCK_NVML_CONFIG", configPath)

	config := LoadConfig()
	if config.NumDevices != 3 {
		t.Errorf("Expected NumDevices=3 from device list, got %d", config.NumDevices)
	}
}

func TestDiscoverConfigPath_NonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Test only applies to non-Linux platforms")
	}
	result := discoverConfigPath()
	if result != "" {
		t.Errorf("Expected empty string on non-Linux, got %q", result)
	}
}

func TestDiscoverConfigPath_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}
	// On Linux without a mock .so loaded, should return empty
	result := discoverConfigPath()
	if result != "" {
		t.Errorf("Expected empty string when no libnvidia-ml.so is mapped, got %q", result)
	}
}

func TestLoadConfig_AutoDiscoverFallback(t *testing.T) {
	// When MOCK_NVML_CONFIG is not set and auto-discovery fails,
	// should fall back to env vars / defaults
	ClearConfigCache()

	config := LoadConfig()
	if config == nil {
		t.Fatal("LoadConfig returned nil")
	}
	if config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", config.NumDevices)
	}
}
