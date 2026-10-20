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

	// 418.x additions
	"nvmlDeviceGetProcessUtilization": {Added: "418.0"},

	// 450.x additions
	"nvmlDeviceGetRemappedRows":    {Added: "450.0"},
	"nvmlDeviceGetRemappedRows_v2": {Added: "450.0"},

	// 470.x additions
	"nvmlDeviceGetArchitecture":             {Added: "470.0"},
	"nvmlDeviceGetGpuMaxPcieLinkGeneration": {Added: "470.0"},

	// 510.x additions
	"nvmlDeviceGetMemoryInfo_v2":               {Added: "510.0"},
	"nvmlDeviceGetComputeRunningProcesses_v3":  {Added: "510.0"},
	"nvmlDeviceGetGraphicsRunningProcesses_v3": {Added: "510.0"},
	"nvmlDeviceGetGspFirmwareMode":             {Added: "510.0"},

	// 515.x additions. The fan family arrived in three driver branches, and
	// the split is worth keeping: a consumer built against a 570 header and
	// run on a 515 profile must be told the cooler symbols are absent
	// rather than that the board declines them, which is the difference
	// between "this driver is too old" and "this GPU has no fans".
	"nvmlDeviceSetFanSpeed_v2":        {Added: "515.0"},
	"nvmlDeviceSetDefaultFanSpeed_v2": {Added: "515.0"},
	"nvmlDeviceGetMinMaxFanSpeed":     {Added: "515.0"},

	// 520.x additions
	"nvmlDeviceGetTargetFanSpeed":      {Added: "520.0"},
	"nvmlDeviceGetFanControlPolicy_v2": {Added: "520.0"},
	"nvmlDeviceSetFanControlPolicy":    {Added: "520.0"},

	// 525.x additions. Confidential Compute arrived with Hopper CC.
	"nvmlDeviceGetConfComputeMemSizeInfo":          {Added: "525.0"},
	"nvmlDeviceGetConfComputeProtectedMemoryUsage": {Added: "525.0"},

	// 535.x additions. GPM (GPU Performance Monitoring) — DCGM's profiling
	// path on Hopper+ (DCGM_FI_PROF_*).
	"nvmlGpmQueryDeviceSupport": {Added: "535.0"},
	"nvmlGpmSampleAlloc":        {Added: "535.0"},
	"nvmlGpmSampleFree":         {Added: "535.0"},
	"nvmlGpmSampleGet":          {Added: "535.0"},
	"nvmlGpmMetricsGet":         {Added: "535.0"},

	// 555.x additions
	"nvmlGpmQueryIfStreamingEnabled": {Added: "555.0"},
	"nvmlGpmSetStreamingEnabled":     {Added: "555.0"},

	// 560.x additions
	"nvmlDeviceGetPlatformInfo": {Added: "560.0"},

	// 565.x additions. The cooler descriptors came a branch later than the
	// rest of the fan family.
	"nvmlDeviceGetCoolerInfo":  {Added: "565.0"},
	"nvmlDeviceGetFanSpeedRPM": {Added: "565.0"},

	// 570.x additions. Workload power profiles arrived with Blackwell; the
	// architecture half of that is the profile's own config, which decides
	// between NOT_SUPPORTED and a profile list. This registry only answers
	// the driver half: on an older driver the symbol did not exist at all,
	// so `nvidia-smi power-profiles` must fail to find it rather than be
	// told the device declines.
	"nvmlDeviceWorkloadPowerProfileGetProfilesInfo":        {Added: "570.0"},
	"nvmlDeviceWorkloadPowerProfileGetCurrentProfiles":     {Added: "570.0"},
	"nvmlDeviceWorkloadPowerProfileSetRequestedProfiles":   {Added: "570.0"},
	"nvmlDeviceWorkloadPowerProfileClearRequestedProfiles": {Added: "570.0"},
	"nvmlDeviceWorkloadPowerProfileUpdateProfiles_v1":      {Added: "570.0"},

	// Power smoothing is the same split: Blackwell in the profile, 570 here.
	"nvmlDevicePowerSmoothingSetState":                 {Added: "570.0"},
	"nvmlDevicePowerSmoothingActivatePresetProfile":    {Added: "570.0"},
	"nvmlDevicePowerSmoothingUpdatePresetProfileParam": {Added: "570.0"},

	// 580.x / NVML 13.0 additions
	"nvmlDeviceGetPowerMizerMode_v1":          {Added: "580.0"},
	"nvmlDeviceSetPowerMizerMode_v1":          {Added: "580.0"},
	"nvmlDeviceGetVgpuSchedulerLog_v2":        {Added: "580.0"},
	"nvmlDeviceGetVgpuSchedulerState_v2":      {Added: "580.0"},
	"nvmlDeviceSetVgpuSchedulerState_v2":      {Added: "580.0"},
	"nvmlDeviceVgpuForceGspUnload":            {Added: "580.0"},
	"nvmlGpuInstanceGetVgpuSchedulerLog_v2":   {Added: "580.0"},
	"nvmlGpuInstanceGetVgpuSchedulerState_v2": {Added: "580.0"},
	"nvmlGpuInstanceSetVgpuSchedulerState_v2": {Added: "580.0"},

	// Device identity and diagnostics. nvml.h carries no version markers, so
	// each date is the first go-nvml header declaring the symbol, mapped to
	// the driver branch its CUDA release shipped with: 12.0 is r525, 12.4
	// r550, 12.6 r560, 12.9 r575, 13.0 r580, 13.1 r590. The A100 capture shows
	// a 570 nvidia-smi rendering an addressing mode before the header declared
	// the call, but the mock's nvidia-smi reaches it through the public symbol,
	// so the header date is what decides. CUDA 13.3 names no driver branch:
	// 600.0 for BBXTimeData_v1 records only that the r595 header lacks it.
	"nvmlDeviceClearFieldValues":    {Added: "525.0"},
	"nvmlDeviceGetLastBBXFlushTime": {Added: "550.0"},
	"nvmlDeviceGetCapabilities":     {Added: "560.0"},
	"nvmlDeviceGetHandleByUUIDV":    {Added: "575.0"},
	"nvmlDeviceGetAddressingMode":   {Added: "580.0"},
	"nvmlDeviceGetPdi":              {Added: "580.0"},
	"nvmlDeviceReadWritePRM_v1":     {Added: "580.0"},
	"nvmlDeviceReadPRMCounters_v1":  {Added: "590.0"},
	"nvmlDeviceGetBBXTimeData_v1":   {Added: "600.0"},

	// Topology functions (available since early NVML)
	"nvmlDeviceGetTopologyCommonAncestor": {Added: "331.0"},
	"nvmlDeviceGetTopologyNearestGpus":    {Added: "331.0"},
	"nvmlSystemGetTopologyGpuSet":         {Added: "331.0"},

	// NVLink functions (390.x+)
	"nvmlDeviceGetNvLinkState":                 {Added: "390.0"},
	"nvmlDeviceGetNvLinkVersion":               {Added: "390.0"},
	"nvmlDeviceGetNvLinkCapability":            {Added: "390.0"},
	"nvmlDeviceGetNvLinkErrorCounter":          {Added: "390.0"},
	"nvmlDeviceGetNvLinkRemotePciInfo_v2":      {Added: "390.0"},
	"nvmlDeviceGetNvLinkRemoteDeviceType":      {Added: "450.0"},
	"nvmlDeviceGetNvLinkUtilizationCounter":    {Added: "390.0"},
	"nvmlDeviceFreezeNvLinkUtilizationCounter": {Added: "390.0"},
	"nvmlDeviceResetNvLinkUtilizationCounter":  {Added: "390.0"},
	"nvmlDeviceResetNvLinkErrorCounters":       {Added: "390.0"},

	// NVLink Reduced Bandwidth Mode, NVLE, and the low-power threshold.
	// nvml.h carries no version markers for these, so the values below are
	// inferences from the architecture era each API targets, not verified
	// release facts: the system-wide pair and the low-power threshold are
	// documented Hopper+, the device-level bw-mode trio Blackwell+, and
	// NvLinkInfo/NVLE arrived with the NVML 13.0 header.
	"nvmlDeviceSetNvLinkDeviceLowPowerThreshold": {Added: "525.0"},
	"nvmlSystemGetNvlinkBwMode":                  {Added: "535.0"},
	"nvmlSystemSetNvlinkBwMode":                  {Added: "535.0"},
	"nvmlDeviceGetNvlinkBwMode":                  {Added: "570.0"},
	"nvmlDeviceSetNvlinkBwMode":                  {Added: "570.0"},
	"nvmlDeviceGetNvlinkSupportedBwModes":        {Added: "570.0"},
	"nvmlDeviceGetNvLinkInfo":                    {Added: "580.0"},

	// CPU / memory affinity functions
	"nvmlDeviceGetCpuAffinity":            {Added: "331.0"},
	"nvmlDeviceGetCpuAffinityWithinScope": {Added: "440.0"},
	"nvmlDeviceGetMemoryAffinity":         {Added: "440.0"},

	// Thermal functions (331.x+)
	"nvmlDeviceGetTemperatureThreshold": {Added: "331.0"},
	"nvmlDeviceGetThermalSettings":      {Added: "331.0"},

	// Power functions (331.x+)
	"nvmlDeviceGetEnforcedPowerLimit":  {Added: "331.0"},
	"nvmlDeviceGetPowerManagementMode": {Added: "331.0"},
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

// FieldAvailable reports whether the given driver version knows a field id.
// Power smoothing arrived in 570 as a unit, the field ids that read it with the
// setters that write it, so a driver without the setters reports none of the
// fields rather than a feature nothing can control. Other ids default to
// available, as unknown functions do.
func FieldAvailable(fieldID uint32, driverVersion string) bool {
	if isPowerSmoothingField(fieldID) {
		return FunctionAvailable("nvmlDevicePowerSmoothingSetState", driverVersion)
	}
	return true
}

// CompareDriverVersions compares two NVIDIA driver version strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Supports formats: "550", "550.163", "550.163.01"
func CompareDriverVersions(a, b string) int {
	aParts := parseVersion(a)
	bParts := parseVersion(b)

	for i := range 3 {
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
