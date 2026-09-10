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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
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
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0o644), "Failed to write config file")

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
	require.NoError(t, os.WriteFile(configPath, []byte(yamlContent), 0o644), "Failed to write config file")

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

// Per-device processes, decoded from real YAML through the inline-embedded
// DeviceConfig and merged: covers override (d0), explicit-clear (d1), inherit (d2).
func TestYAMLConfig_PerDeviceProcesses(t *testing.T) {
	const y = `
device_defaults:
  processes:
    - {pid: 1, type: "C"}
devices:
  - index: 0
    processes:
      - {pid: 4242, type: "C", sm_util: 75}
  - index: 1
    processes: []
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")
	c := &Config{YAMLConfig: &yc}

	d0 := c.GetDeviceConfig(0)
	require.Len(t, d0.Processes, 1, "device 0 (override) len")
	require.Equal(t, uint32(4242), d0.Processes[0].PID, "device 0 (override) PID")
	require.Equal(t, uint32(75), d0.Processes[0].SmUtil, "device 0 (override) SmUtil")

	d1 := c.GetDeviceConfig(1) // processes: [] clears the default
	require.Empty(t, d1.Processes, "device 1 (explicit clear)")

	d2 := c.GetDeviceConfig(2) // no override -> inherit
	require.Len(t, d2.Processes, 1, "device 2 (inherit) len")
	require.Equal(t, uint32(1), d2.Processes[0].PID, "device 2 (inherit) PID")
}

// A device that does not declare minor_number takes the index, which is what
// the driver assigns when probe order follows PCI enumeration order. Treating
// an omitted key as minor 0 would point every such device at /dev/nvidia0.
func TestGetDeviceMinorNumber_DefaultsToIndex(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    uuid: "GPU-aaa"
  - index: 1
    uuid: "GPU-bbb"
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")
	c := &Config{YAMLConfig: &yc}

	require.Equal(t, 0, c.GetDeviceMinorNumber(0))
	require.Equal(t, 1, c.GetDeviceMinorNumber(1))
	require.Equal(t, 2, c.GetDeviceMinorNumber(2), "device without an override entry")
}

// Nodes whose driver probe order does not follow PCI enumeration order report
// minor numbers that do not match the NVML index, so the profile must be able
// to say so — including minor 0 on a device that is not index 0.
func TestGetDeviceMinorNumber_HonorsExplicitValue(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: 2
  - index: 1
    minor_number: 0
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")
	c := &Config{YAMLConfig: &yc}

	require.Equal(t, 2, c.GetDeviceMinorNumber(0))
	require.Equal(t, 0, c.GetDeviceMinorNumber(1))
}

// Two devices claiming one minor number would collapse onto a single
// /dev/nvidia<N>, silently handing both GPUs the same character device.
func TestValidateYAMLConfig_RejectsDuplicateMinorNumbers(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: 3
  - index: 1
    minor_number: 3
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc), "duplicate device minor number: 3")
}

// A device that omits minor_number still occupies its index as a minor, so an
// explicit value elsewhere can collide with it. Checking only the values that
// were spelled out would let both devices reach the same /dev/nvidia<N>.
func TestValidateYAMLConfig_RejectsCollisionWithADefaultedMinorNumber(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: 1
  - index: 1
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc), "duplicate device minor number: 1")
}

// An implicit device — one the count covers but no entry describes — takes its
// index as a minor and can be collided with just the same.
func TestValidateYAMLConfig_RejectsCollisionWithAnUndeclaredDevice(t *testing.T) {
	t.Parallel()

	y := `
version: "1.0"
system:
  driver_version: "550.163.01"
  num_devices: 4
devices:
  - index: 0
    minor_number: 3
`
	var yc YAMLConfig
	require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

	require.ErrorContains(t, validateYAMLConfig(&yc), "duplicate device minor number: 3")
}

// stageCharDevs formats the node name from the minor and casts it to uint32.
// A negative value names a node the GPU-node pattern cannot match, so it
// survives pruning, and 255 is nvidiactl's.
func TestValidateYAMLConfig_RejectsMinorNumbersOutsideTheDeviceRange(t *testing.T) {
	t.Parallel()

	for name, minor := range map[string]int{"negative": -1, "nvidiactl": 255} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			y := fmt.Sprintf(`
version: "1.0"
system:
  driver_version: "550.163.01"
devices:
  - index: 0
    minor_number: %d
`, minor)
			var yc YAMLConfig
			require.NoError(t, yaml.Unmarshal([]byte(y), &yc), "yaml decode")

			require.ErrorContains(t, validateYAMLConfig(&yc), "device minor number out of range")
		})
	}
}

func TestValidateMIGConfig_ExplicitInstances(t *testing.T) {
	t.Parallel()

	start := 0
	tests := []struct {
		name    string
		mig     *MIGConfig
		wantErr string
	}{
		{
			name: "valid explicit layout",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{
					{ID: 0, Profile: "1g.5gb", PlacementStart: &start},
					{ID: 2, Profile: "1g.5gb"},
				},
			},
		},
		{
			name: "empty list is valid and means no partitions",
			mig:  &MIGConfig{ModeCurrent: "enabled", Instances: &[]MIGGPUInstanceRecord{}},
		},
		{
			name: "duplicate instance ids",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{
					{ID: 1, Profile: "1g.5gb"},
					{ID: 1, Profile: "1g.5gb"},
				},
			},
			wantErr: "duplicate GPU instance id 1",
		},
		{
			name: "neither profile nor profile_id",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances:   &[]MIGGPUInstanceRecord{{ID: 0}},
			},
			wantErr: "must set either profile or profile_id",
		},
		{
			name: "duplicate compute instance ids",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{{
					ID: 0, Profile: "1g.5gb",
					ComputeInstances: &[]MIGComputeInstanceRecord{{ID: 0, Profile: "1c"}, {ID: 0, Profile: "1c"}},
				}},
			},
			wantErr: "duplicate compute instance id 0",
		},
		{
			name: "empty compute instance list is valid and means none exist",
			mig: &MIGConfig{
				ModeCurrent: "enabled",
				Instances: &[]MIGGPUInstanceRecord{{
					ID: 0, Profile: "1g.5gb",
					ComputeInstances: &[]MIGComputeInstanceRecord{},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMIGConfig(tt.mig)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// The distinction the pointer exists for: absent means "no explicit layout",
// present-and-empty means "an explicit layout with nothing in it".
func TestMIGConfig_EmptyInstancesRoundTripsDistinctFromAbsent(t *testing.T) {
	t.Parallel()

	var absent MIGConfig
	require.NoError(t, yaml.Unmarshal([]byte("mode_current: enabled\n"), &absent))
	require.Nil(t, absent.Instances)

	var empty MIGConfig
	require.NoError(t, yaml.Unmarshal([]byte("mode_current: enabled\ninstances: []\n"), &empty))
	require.NotNil(t, empty.Instances)
	require.Empty(t, *empty.Instances)
}
