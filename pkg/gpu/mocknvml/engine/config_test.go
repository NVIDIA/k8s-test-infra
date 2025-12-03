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
	if config.DriverVersion != "550.54.15" {
		t.Errorf("Expected default DriverVersion 550.54.15, got %s", config.DriverVersion)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear any existing env vars
	_ = os.Unsetenv("MOCK_NVML_NUM_DEVICES")
	_ = os.Unsetenv("MOCK_NVML_DRIVER_VERSION")

	config := LoadConfig()
	if config == nil {
		t.Fatal("LoadConfig returned nil")
	}
	if config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", config.NumDevices)
	}
	if config.DriverVersion != "550.54.15" {
		t.Errorf("Expected default DriverVersion, got %s", config.DriverVersion)
	}
}

func TestLoadConfig_NumDevices(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected int
	}{
		{"Valid number", "4", 4},
		{"Zero devices", "0", 0},
		{"Max devices", "8", 8},
		{"Invalid string", "abc", 8}, // Should use default
		{"Negative number", "-1", 8}, // Should use default
		{"Empty string", "", 8},      // Should use default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				_ = os.Setenv("MOCK_NVML_NUM_DEVICES", tt.envValue)
			} else {
				_ = os.Unsetenv("MOCK_NVML_NUM_DEVICES")
			}
			defer func() { _ = os.Unsetenv("MOCK_NVML_NUM_DEVICES") }()

			config := LoadConfig()
			if config.NumDevices != tt.expected {
				t.Errorf("Expected NumDevices %d, got %d", tt.expected, config.NumDevices)
			}
		})
	}
}

func TestLoadConfig_DriverVersion(t *testing.T) {
	customVersion := "999.99.99"
	_ = os.Setenv("MOCK_NVML_DRIVER_VERSION", customVersion)
	defer func() { _ = os.Unsetenv("MOCK_NVML_DRIVER_VERSION") }()

	config := LoadConfig()
	if config.DriverVersion != customVersion {
		t.Errorf("Expected DriverVersion %s, got %s", customVersion, config.DriverVersion)
	}
}

func TestLoadConfig_AllEnvVars(t *testing.T) {
	_ = os.Setenv("MOCK_NVML_NUM_DEVICES", "6")
	_ = os.Setenv("MOCK_NVML_DRIVER_VERSION", "600.00.00")
	defer func() {
		_ = os.Unsetenv("MOCK_NVML_NUM_DEVICES")
		_ = os.Unsetenv("MOCK_NVML_DRIVER_VERSION")
	}()

	config := LoadConfig()
	if config.NumDevices != 6 {
		t.Errorf("NumDevices not set correctly: %d", config.NumDevices)
	}
	if config.DriverVersion != "600.00.00" {
		t.Errorf("DriverVersion not set correctly: %s", config.DriverVersion)
	}
}

func TestLoadConfig_EmptyEnvVars(t *testing.T) {
	_ = os.Setenv("MOCK_NVML_NUM_DEVICES", "")
	_ = os.Setenv("MOCK_NVML_DRIVER_VERSION", "")
	defer func() {
		_ = os.Unsetenv("MOCK_NVML_NUM_DEVICES")
		_ = os.Unsetenv("MOCK_NVML_DRIVER_VERSION")
	}()

	config := LoadConfig()
	// Empty strings should result in defaults
	if config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", config.NumDevices)
	}
	if config.DriverVersion != "550.54.15" {
		t.Errorf("Expected default DriverVersion, got %s", config.DriverVersion)
	}
}
