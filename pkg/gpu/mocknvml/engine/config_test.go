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

	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	require.NotNil(t, config, "DefaultConfig returned nil")
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
	require.Equal(t, "550.163.01", config.DriverVersion, "Expected default DriverVersion 550.163.01")
}

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear config cache to ensure clean state
	ClearConfigCache()

	config := LoadConfig()
	require.NotNil(t, config, "LoadConfig returned nil")
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
	require.Equal(t, "550.163.01", config.DriverVersion, "Expected default DriverVersion")
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
			require.Equal(t, tt.expected, config.NumDevices, "Expected NumDevices %d", tt.expected)
		})
	}
}

func TestLoadConfig_DriverVersion(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	customVersion := "999.99.99"
	t.Setenv("MOCK_NVML_DRIVER_VERSION", customVersion)

	config := LoadConfig()
	require.Equal(t, customVersion, config.DriverVersion, "Expected DriverVersion %s", customVersion)
}

func TestLoadConfig_AllEnvVars(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "6")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "600.00.00")

	config := LoadConfig()
	require.Equal(t, 6, config.NumDevices, "NumDevices not set correctly")
	require.Equal(t, "600.00.00", config.DriverVersion, "DriverVersion not set correctly")
}

func TestLoadConfig_EmptyEnvVars(t *testing.T) {
	// Clear config cache to ensure env vars take effect
	ClearConfigCache()

	t.Setenv("MOCK_NVML_NUM_DEVICES", "")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "")

	config := LoadConfig()
	// Empty strings should result in defaults
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
	require.Equal(t, "550.163.01", config.DriverVersion, "Expected default DriverVersion")
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
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0644), "Failed to write config file")

	ClearConfigCache()
	t.Setenv("MOCK_NVML_CONFIG", configPath)

	config := LoadConfig()
	require.Equal(t, 4, config.NumDevices, "Expected NumDevices=4 from system.num_devices")
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
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0644), "Failed to write config file")

	ClearConfigCache()
	t.Setenv("MOCK_NVML_CONFIG", configPath)

	config := LoadConfig()
	require.Equal(t, 3, config.NumDevices, "Expected NumDevices=3 from device list")
}

func TestDiscoverConfigPath_NonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Test only applies to non-Linux platforms")
	}
	result := discoverConfigPath()
	require.Empty(t, result, "Expected empty string on non-Linux")
}

func TestDiscoverConfigPath_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}
	// On Linux without a mock .so loaded, should return empty
	result := discoverConfigPath()
	require.Empty(t, result, "Expected empty string when no libnvidia-ml.so is mapped")
}

func TestLoadConfig_AutoDiscoverFallback(t *testing.T) {
	// When MOCK_NVML_CONFIG is not set and auto-discovery fails,
	// should fall back to env vars / defaults
	ClearConfigCache()

	config := LoadConfig()
	require.NotNil(t, config, "LoadConfig returned nil")
	require.Equal(t, 8, config.NumDevices, "Expected default NumDevices 8")
}

func TestGetDeviceConfig_PerDeviceProcessesOverride(t *testing.T) {
	c := &Config{
		YAMLConfig: &YAMLConfig{
			DeviceDefaults: DeviceConfig{
				Processes: []ProcessConfig{{PID: 1, Type: "C", UsedMemoryMiB: 100}},
			},
			Devices: []DeviceOverride{
				{
					Index: 0,
					DeviceConfig: DeviceConfig{
						Processes: []ProcessConfig{{PID: 4242, Type: "C", UsedMemoryMiB: 6000}},
					},
				},
				{Index: 1}, // no override -> inherits device_defaults
			},
		},
	}

	// Device 0 uses its override.
	d0 := c.GetDeviceConfig(0)
	if len(d0.Processes) != 1 || d0.Processes[0].PID != 4242 {
		t.Fatalf("Device 0: expected overridden PID 4242, got %+v", d0.Processes)
	}
	// Device 1 inherits the default.
	d1 := c.GetDeviceConfig(1)
	if len(d1.Processes) != 1 || d1.Processes[0].PID != 1 {
		t.Fatalf("Device 1: expected inherited PID 1, got %+v", d1.Processes)
	}
}
