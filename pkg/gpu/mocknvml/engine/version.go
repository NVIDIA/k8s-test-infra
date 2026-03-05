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
	"strconv"
	"strings"
)

// FunctionVersion describes the driver version range where an NVML function exists.
type FunctionVersion struct {
	Added   string // Driver version where function was introduced (e.g., "470.0")
	Removed string // Driver version where function was removed (empty = still present)
}

// functionRegistry maps NVML function names to their version boundaries.
// Unknown functions default to "available" for backwards compatibility.
var functionRegistry = map[string]FunctionVersion{
	// Core functions (331.x)
	"nvmlInit_v2":                   {Added: "331.0"},
	"nvmlDeviceGetCount_v2":         {Added: "331.0"},
	"nvmlDeviceGetHandleByIndex_v2": {Added: "331.0"},

	// 450.x additions
	"nvmlDeviceGetRemappedRows": {Added: "450.0"},

	// 470.x additions
	"nvmlDeviceGetArchitecture":             {Added: "470.0"},
	"nvmlDeviceGetGpuMaxPcieLinkGeneration": {Added: "470.0"},

	// 510.x additions
	"nvmlDeviceGetMemoryInfo_v2":               {Added: "510.0"},
	"nvmlDeviceGetComputeRunningProcesses_v3":  {Added: "510.0"},
	"nvmlDeviceGetGraphicsRunningProcesses_v3": {Added: "510.0"},
	"nvmlDeviceGetGspFirmwareMode":             {Added: "510.0"},

	// 535.x additions
	"nvmlGpmQueryDeviceSupport": {Added: "535.0"},

	// 560.x additions
	"nvmlDeviceGetPlatformInfo": {Added: "560.0"},
}

// GetFunctionRegistry returns a copy of the function version registry.
func GetFunctionRegistry() map[string]FunctionVersion {
	result := make(map[string]FunctionVersion, len(functionRegistry))
	for k, v := range functionRegistry {
		result[k] = v
	}
	return result
}

// FunctionAvailable returns true if the named function exists in the given driver version.
// Unknown functions default to available (backwards compatibility).
func FunctionAvailable(funcName, driverVersion string) bool {
	entry, exists := functionRegistry[funcName]
	if !exists {
		return true
	}
	if CompareDriverVersions(driverVersion, entry.Added) < 0 {
		return false
	}
	if entry.Removed != "" && CompareDriverVersions(driverVersion, entry.Removed) >= 0 {
		return false
	}
	return true
}

// CompareDriverVersions compares two NVIDIA driver version strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Supports formats: "550", "550.163", "550.163.01"
func CompareDriverVersions(a, b string) int {
	aParts := parseVersion(a)
	bParts := parseVersion(b)

	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

// parseVersion splits a version string into [major, minor, patch].
// Missing components default to 0.
func parseVersion(v string) [3]int {
	var result [3]int
	parts := strings.SplitN(v, ".", 3)
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n, _ := strconv.Atoi(p)
		result[i] = n
	}
	return result
}
