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
	"strconv"
)

// maxConfigDevices is the maximum number of devices that can be configured.
// This matches the DGX A100 configuration from the upstream mock.
const maxConfigDevices = 8

// Config holds configuration for the mock engine
type Config struct {
	NumDevices    int
	DriverVersion string
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		NumDevices:    8, // Default to DGX A100 behavior
		DriverVersion: "550.54.15",
	}
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	config := DefaultConfig()

	// Override device count if specified (capped at maxConfigDevices)
	if num := os.Getenv("MOCK_NVML_NUM_DEVICES"); num != "" {
		if val, err := strconv.Atoi(num); err == nil && val >= 0 && val <= maxConfigDevices {
			config.NumDevices = val
		}
	}

	// Override driver version
	if ver := os.Getenv("MOCK_NVML_DRIVER_VERSION"); ver != "" {
		config.DriverVersion = ver
	}

	return config
}
