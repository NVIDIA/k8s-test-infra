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

// Config contains configuration options for the mock NVML engine.
type Config struct {
	// NumDevices overrides the default number of devices (8).
	// If 0 or negative, uses the default from dgxa100.
	NumDevices int

	// DriverVersion overrides the default driver version.
	// If empty, uses the default from dgxa100.
	DriverVersion string

	// NVMLVersion overrides the default NVML version.
	// If empty, uses the default from dgxa100.
	NVMLVersion string

	// CudaDriverVersion overrides the default CUDA driver version.
	// If 0, uses the default from dgxa100.
	CudaDriverVersion int
}

// DefaultConfig returns the default configuration.
// The default configuration matches the dgxa100 mock.
func DefaultConfig() *Config {
	cfg := &Config{
		NumDevices:        8,
		DriverVersion:     "550.54.15",
		NVMLVersion:       "12.550.54.15",
		CudaDriverVersion: 12040,
	}

	// Allow override via environment variables
	cfg.loadFromEnv()
	return cfg
}

// loadFromEnv loads configuration from environment variables.
func (c *Config) loadFromEnv() {
	if val := os.Getenv("MOCK_NVML_NUM_DEVICES"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			c.NumDevices = n
		}
	}

	if val := os.Getenv("MOCK_NVML_DRIVER_VERSION"); val != "" {
		c.DriverVersion = val
	}

	if val := os.Getenv("MOCK_NVML_NVML_VERSION"); val != "" {
		c.NVMLVersion = val
	}

	if val := os.Getenv("MOCK_NVML_CUDA_VERSION"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			c.CudaDriverVersion = n
		}
	}
}

