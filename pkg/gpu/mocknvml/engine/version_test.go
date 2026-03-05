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

import "testing"

func TestCompareDriverVersions(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int // -1, 0, 1
	}{
		// Equal versions
		{"equal full", "550.163.01", "550.163.01", 0},
		{"equal major only", "550", "550", 0},
		{"equal major.minor", "550.163", "550.163", 0},

		// Major version differences
		{"major less", "550.163.01", "560.28.03", -1},
		{"major greater", "560.28.03", "550.163.01", 1},

		// Minor version differences
		{"minor less", "550.100.01", "550.163.01", -1},
		{"minor greater", "550.163.01", "550.100.01", 1},

		// Patch version differences
		{"patch less", "550.163.01", "550.163.02", -1},
		{"patch greater", "550.163.02", "550.163.01", 1},

		// Mixed formats (major-only vs full)
		{"major-only vs full equal major", "550", "550.163.01", -1},
		{"full vs major-only equal major", "550.163.01", "550", 1},
		{"major-only vs major.minor", "550", "550.0", 0},

		// Edge cases
		{"leading zeros in patch", "550.163.01", "550.163.1", 0},
		{"zero patch", "550.163.0", "550.163", 0},

		// Boundary versions from NVML data
		{"331 vs 470", "331.0", "470.0", -1},
		{"510 vs 535", "510.0", "535.0", -1},
		{"535 vs 560", "535.0", "560.0", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareDriverVersions(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("CompareDriverVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFunctionAvailable(t *testing.T) {
	tests := []struct {
		name          string
		driverVersion string
		funcName      string
		want          bool
	}{
		// Function added in 470.x, configured at 550.x -> available
		{"available after added", "550.163.01", "nvmlDeviceGetArchitecture", true},

		// Function added in 560.x, configured at 550.x -> NOT available
		{"not available before added", "550.163.01", "nvmlDeviceGetPlatformInfo", false},

		// Function added in 510.x, configured at 510.x -> available (exact match)
		{"available at exact version", "510.0", "nvmlDeviceGetMemoryInfo_v2", true},

		// Function added in 510.x, configured at 470.x -> NOT available
		{"not available before version", "470.0", "nvmlDeviceGetMemoryInfo_v2", false},

		// Unknown function -> defaults to available (backwards compat)
		{"unknown function available", "550.163.01", "nvmlSomeUnknownFunction", true},

		// Very old driver, basic functions still available
		{"basic function on old driver", "331.0", "nvmlInit_v2", true},

		// Function added in 535.x
		{"gpm not available on 510", "510.0", "nvmlGpmQueryDeviceSupport", false},
		{"gpm available on 535", "535.0", "nvmlGpmQueryDeviceSupport", true},
		{"gpm available on 560", "560.0", "nvmlGpmQueryDeviceSupport", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FunctionAvailable(tt.funcName, tt.driverVersion)
			if got != tt.want {
				t.Errorf("FunctionAvailable(%q, %q) = %v, want %v", tt.funcName, tt.driverVersion, got, tt.want)
			}
		})
	}
}

func TestFunctionRegistry_Coverage(t *testing.T) {
	// Verify that expected functions exist in the registry
	expectedFunctions := []string{
		"nvmlInit_v2",
		"nvmlDeviceGetCount_v2",
		"nvmlDeviceGetHandleByIndex_v2",
		"nvmlDeviceGetMemoryInfo_v2",
		"nvmlDeviceGetArchitecture",
		"nvmlDeviceGetComputeRunningProcesses_v3",
		"nvmlDeviceGetGraphicsRunningProcesses_v3",
		"nvmlDeviceGetGpuMaxPcieLinkGeneration",
		"nvmlDeviceGetRemappedRows",
		"nvmlDeviceGetGspFirmwareMode",
		"nvmlGpmQueryDeviceSupport",
		"nvmlDeviceGetPlatformInfo",
	}

	registry := GetFunctionRegistry()
	for _, name := range expectedFunctions {
		if _, ok := registry[name]; !ok {
			t.Errorf("expected function %q not found in registry", name)
		}
	}
}
