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
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// TestBuildFabricInfo_DefaultsToHealthyFabric pins the behaviour issue #677
// exists for: a fabric block that says nothing about health must report a
// healthy fabric, not an unknown one. A zero summary makes nvidia-smi print
// N/A for the whole Fabric.Health block, so the default has to be non-zero.
func TestBuildFabricInfo_DefaultsToHealthyFabric(t *testing.T) {
	info := buildFabricInfo(&FabricConfig{})

	require.Equal(t, uint8(nvml.GPU_FABRIC_HEALTH_SUMMARY_HEALTHY), info.HealthSummary, "health summary")
	require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_FALSE),
		decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_DEGRADED_BW), "degraded bandwidth")
	require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_FALSE),
		decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_RECOVERY), "route recovery")
	require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE),
		decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY), "route unhealthy")
	require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_FALSE),
		decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ACCESS_TIMEOUT_RECOVERY), "access timeout recovery")
	require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE),
		decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_INCORRECT_CONFIGURATION,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_INCORRECT_CONFIGURATION), "incorrect configuration")

	// Real hardware leaves this field unanswered (nvidia-smi renders the row
	// as N/A on a healthy GB300 tray), so the mock must not claim otherwise.
	require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_PARTITION_ASSIGNED_NOT_SUPPORTED),
		decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_PARTITION_ASSIGNED,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_PARTITION_ASSIGNED), "partition assigned")
}

// TestBuildFabricInfo_ConditionFlipsOnlyItsOwnField is the assertion a
// wholesale mask would pass and must not: injecting one condition has to leave
// every neighbouring condition reporting False, not NOT_SUPPORTED and not
// True. Each case also pins the summary the condition implies.
func TestBuildFabricInfo_ConditionFlipsOnlyItsOwnField(t *testing.T) {
	type condition struct {
		shift, width int
		wantTrue     uint32
	}
	all := map[string]condition{
		"degraded_bandwidth": {
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_DEGRADED_BW,
			nvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_TRUE,
		},
		"route_recovery": {
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_TRUE,
		},
		"route_unhealthy": {
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY,
			nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_TRUE,
		},
		"access_timeout_recovery": {
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ACCESS_TIMEOUT_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_TRUE,
		},
	}
	// The False value is identical for all four conditions, so one constant
	// covers the "neighbour unchanged" half of every case.
	const wantFalse = uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE)

	cases := map[string]struct {
		health      FabricHealthConfig
		wantSummary uint8
	}{
		"degraded_bandwidth": {
			FabricHealthConfig{DegradedBandwidth: true},
			// Reduced bandwidth still routes, so the fabric is usable.
			FabricHealthSummaryLimitedCapacity,
		},
		"route_recovery":          {FabricHealthConfig{RouteRecovery: true}, FabricHealthSummaryUnhealthy},
		"route_unhealthy":         {FabricHealthConfig{RouteUnhealthy: true}, FabricHealthSummaryUnhealthy},
		"access_timeout_recovery": {FabricHealthConfig{AccessTimeoutRecovery: true}, FabricHealthSummaryUnhealthy},
	}

	for injected, tc := range cases {
		t.Run(injected, func(t *testing.T) {
			health := tc.health
			info := buildFabricInfo(&FabricConfig{Health: &health})

			require.Equal(t, tc.wantSummary, info.HealthSummary, "summary")
			for name, c := range all {
				got := decodeFabricHealth(info.HealthMask, c.shift, c.width)
				if name == injected {
					require.Equal(t, c.wantTrue, got, "%s should report the injected fault", name)
					continue
				}
				require.Equal(t, wantFalse, got, "%s must stay healthy when only %s was injected", name, injected)
			}
			require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE),
				decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_INCORRECT_CONFIGURATION,
					nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_INCORRECT_CONFIGURATION),
				"incorrect configuration must stay None when only %s was injected", injected)
		})
	}
}

func TestBuildFabricInfo_IncorrectConfiguration(t *testing.T) {
	cases := map[string]struct {
		configured  string
		wantValue   uint32
		wantSummary uint8
	}{
		"named misconfiguration": {
			"no_partition",
			nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NO_PARTITION,
			FabricHealthSummaryUnhealthy,
		},
		"explicit none": {
			"none",
			nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE,
			FabricHealthSummaryHealthy,
		},
		// A typo must not silently downgrade a fabric to unhealthy, matching
		// how parseFabricState treats an unknown state.
		"unknown name falls back to none": {
			"nonsense",
			nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE,
			FabricHealthSummaryHealthy,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			info := buildFabricInfo(&FabricConfig{
				Health: &FabricHealthConfig{IncorrectConfiguration: tc.configured},
			})
			require.Equal(t, tc.wantValue,
				decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_INCORRECT_CONFIGURATION,
					nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_INCORRECT_CONFIGURATION), "incorrect configuration")
			require.Equal(t, tc.wantSummary, info.HealthSummary, "summary")
		})
	}
}

// TestBuildFabricInfo_ExplicitSummaryWins covers pinning a summary that the
// conditions do not imply — a driver that reports Unhealthy without saying
// which condition failed, which is a shape consumers have to tolerate.
func TestBuildFabricInfo_ExplicitSummaryWins(t *testing.T) {
	info := buildFabricInfo(&FabricConfig{HealthSummary: "unhealthy"})
	require.Equal(t, FabricHealthSummaryUnhealthy, info.HealthSummary, "pinned summary")
	require.Equal(t, uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE),
		decodeFabricHealth(info.HealthMask, nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY),
		"pinning the summary must not invent a condition")

	// "auto" is the explicit spelling of the default and must derive.
	derived := buildFabricInfo(&FabricConfig{
		HealthSummary: "auto",
		Health:        &FabricHealthConfig{RouteUnhealthy: true},
	})
	require.Equal(t, FabricHealthSummaryUnhealthy, derived.HealthSummary, "auto derives from the conditions")

	// A pinned summary of not_supported is how a profile asks for the old
	// all-N/A rendering back.
	off := buildFabricInfo(&FabricConfig{HealthSummary: "not_supported"})
	require.Equal(t, FabricHealthSummaryNotSupported, off.HealthSummary, "not_supported")
}

// TestBuildFabricInfo_RawHealthMaskEscapeHatch covers a hand-assembled mask:
// it must reach the consumer verbatim (issue #677: setting it used to be
// silently dropped) and drive the summary, so the two can never disagree.
func TestBuildFabricInfo_RawHealthMaskEscapeHatch(t *testing.T) {
	// Everything healthy except a partition-assigned answer, which the
	// symbolic keys deliberately cannot express.
	raw := uint32(nvml.GPU_FABRIC_HEALTH_MASK_DEGRADED_BW_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW |
		nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_RECOVERY_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY |
		nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY |
		nvml.GPU_FABRIC_HEALTH_MASK_ACCESS_TIMEOUT_RECOVERY_FALSE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY |
		nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_INCORRECT_CONFIGURATION |
		nvml.GPU_FABRIC_HEALTH_MASK_PARTITION_ASSIGNED_TRUE<<nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_PARTITION_ASSIGNED)

	info := buildFabricInfo(&FabricConfig{
		// The raw mask wins over the symbolic keys, so a fault named here
		// must not leak into the reported mask.
		Health:     &FabricHealthConfig{RouteUnhealthy: true},
		HealthMask: &raw,
	})
	require.Equal(t, raw, info.HealthMask, "raw mask must reach the consumer verbatim")
	require.Equal(t, FabricHealthSummaryHealthy, info.HealthSummary, "summary derived from the raw mask")
}

// TestBuildFabricInfo_ZeroHealthMaskReportsNothing keeps the pre-#677
// rendering reachable: an explicitly zeroed mask answers no condition, so the
// summary must stay NOT_SUPPORTED and nvidia-smi keeps printing N/A.
func TestBuildFabricInfo_ZeroHealthMaskReportsNothing(t *testing.T) {
	zero := uint32(0)
	info := buildFabricInfo(&FabricConfig{HealthMask: &zero})
	require.Equal(t, uint32(0), info.HealthMask, "mask")
	require.Equal(t, FabricHealthSummaryNotSupported, info.HealthSummary, "summary")
}

// TestFabricHealthSummaryConstants_MatchGoNVML pins the engine's re-declared
// summary constants against go-nvml, the same guard fabric_layout_test.go
// applies to the struct tags. A drift here would report a plausible-looking
// but wrong summary.
func TestFabricHealthSummaryConstants_MatchGoNVML(t *testing.T) {
	require.Equal(t, FabricHealthSummaryNotSupported, uint8(nvml.GPU_FABRIC_HEALTH_SUMMARY_NOT_SUPPORTED), "not_supported")
	require.Equal(t, FabricHealthSummaryHealthy, uint8(nvml.GPU_FABRIC_HEALTH_SUMMARY_HEALTHY), "healthy")
	require.Equal(t, FabricHealthSummaryUnhealthy, uint8(nvml.GPU_FABRIC_HEALTH_SUMMARY_UNHEALTHY), "unhealthy")
	require.Equal(t, FabricHealthSummaryLimitedCapacity, uint8(nvml.GPU_FABRIC_HEALTH_SUMMARY_LIMITED_CAPACITY), "limited_capacity")
}

// TestFabricHealthFields_MatchGoNVML pins every slot's shift and width, and
// the tri-state values shared by the boolean conditions.
func TestFabricHealthFields_MatchGoNVML(t *testing.T) {
	cases := map[string]struct {
		field        fabricHealthField
		shift, width int
	}{
		"degraded_bw": {
			fabricHealthDegradedBW,
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_DEGRADED_BW,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_DEGRADED_BW,
		},
		"route_recovery": {
			fabricHealthRouteRecovery,
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_RECOVERY,
		},
		"route_unhealthy": {
			fabricHealthRouteUnhealthy,
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ROUTE_UNHEALTHY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ROUTE_UNHEALTHY,
		},
		"access_timeout_recovery": {
			fabricHealthAccessTimeoutRecovery,
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_ACCESS_TIMEOUT_RECOVERY,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_ACCESS_TIMEOUT_RECOVERY,
		},
		"incorrect_configuration": {
			fabricHealthIncorrectConfig,
			nvml.GPU_FABRIC_HEALTH_MASK_SHIFT_INCORRECT_CONFIGURATION,
			nvml.GPU_FABRIC_HEALTH_MASK_WIDTH_INCORRECT_CONFIGURATION,
		},
	}
	for name, tc := range cases {
		require.Equal(t, uint32(tc.shift), tc.field.shift, "%s shift", name)
		require.Equal(t, uint32(tc.width), tc.field.width, "%s width", name)
	}

	require.Equal(t, fabricHealthNotSupported, uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_NOT_SUPPORTED), "not_supported")
	require.Equal(t, fabricHealthTrue, uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_TRUE), "true")
	require.Equal(t, fabricHealthFalse, uint32(nvml.GPU_FABRIC_HEALTH_MASK_ROUTE_UNHEALTHY_FALSE), "false")
	require.Equal(t, fabricIncorrectConfigNone, uint32(nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE), "incorrect configuration none")

	for name, want := range map[string]int{
		"not_supported":        nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NOT_SUPPORTED,
		"none":                 nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NONE,
		"incorrect_sysguid":    nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_INCORRECT_SYSGUID,
		"incorrect_chassis_sn": nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_INCORRECT_CHASSIS_SN,
		"no_partition":         nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_NO_PARTITION,
		"insufficient_nvlinks": nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_INSUFFICIENT_NVLINKS,
		"incompatible_gpu_fw":  nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_INCOMPATIBLE_GPU_FW,
		"invalid_location":     nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_INVALID_LOCATION,
		"gpu_state_invalid":    nvml.GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_GPU_STATE_INVALID,
	} {
		require.Equal(t, uint32(want), fabricIncorrectConfigValues[name], "incorrect configuration %q", name)
	}
}

// decodeFabricHealth reads one condition out of a health mask the way a
// consumer does, via the upstream NVML_GPU_FABRIC_HEALTH_STATUS_GET macro
// (shift then mask by width). Tests decode with the upstream constants so a
// wrong shift in the engine cannot agree with a wrong shift in the test.
func decodeFabricHealth(mask uint32, shift, width int) uint32 {
	return (mask >> uint32(shift)) & uint32(width)
}
