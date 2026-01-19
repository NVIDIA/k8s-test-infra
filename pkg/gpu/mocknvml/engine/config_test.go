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
	// t.Setenv automatically clears env vars for this test scope
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
	customVersion := "999.99.99"
	t.Setenv("MOCK_NVML_DRIVER_VERSION", customVersion)

	config := LoadConfig()
	if config.DriverVersion != customVersion {
		t.Errorf("Expected DriverVersion %s, got %s", customVersion, config.DriverVersion)
	}
}

func TestLoadConfig_AllEnvVars(t *testing.T) {
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
	t.Setenv("MOCK_NVML_NUM_DEVICES", "")
	t.Setenv("MOCK_NVML_DRIVER_VERSION", "")

	config := LoadConfig()
	// Empty strings should result in defaults
	if config.NumDevices != 8 {
		t.Errorf("Expected default NumDevices 8, got %d", config.NumDevices)
	}
	if config.DriverVersion != "550.54.15" {
		t.Errorf("Expected default DriverVersion, got %s", config.DriverVersion)
	}
}
