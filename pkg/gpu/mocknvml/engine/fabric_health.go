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

import "strings"

// FabricHealthSummary* values mirror NVML_GPU_FABRIC_HEALTH_SUMMARY_* from the
// public NVML header, re-declared here for the same reason as FabricState*:
// the engine layer stays free of CGo and callers compare against named
// constants. fabric_health_test.go pins them against go-nvml.
//
// A zero summary means "the driver did not report health", which nvidia-smi
// renders as N/A for every row of the Fabric.Health block — including the
// per-condition rows it would otherwise decode out of the health mask. That is
// why the mock defaults to Healthy rather than to the Go zero value.
const (
	FabricHealthSummaryNotSupported    uint8 = 0
	FabricHealthSummaryHealthy         uint8 = 1
	FabricHealthSummaryUnhealthy       uint8 = 2
	FabricHealthSummaryLimitedCapacity uint8 = 3
)

// Tri-state values every boolean condition in the health mask takes, mirroring
// NVML_GPU_FABRIC_HEALTH_MASK_<CONDITION>_{NOT_SUPPORTED,TRUE,FALSE}. All four
// boolean conditions share this encoding.
const (
	fabricHealthNotSupported uint32 = 0
	fabricHealthTrue         uint32 = 1
	fabricHealthFalse        uint32 = 2
)

// fabricHealthField is one condition's slot in the health mask: a bit offset
// and a width mask, mirroring NVML_GPU_FABRIC_HEALTH_MASK_SHIFT_* and
// _WIDTH_*. Consumers read a slot with the NVML_GPU_FABRIC_HEALTH_STATUS_GET
// macro, which is what get() reproduces.
type fabricHealthField struct {
	shift uint32
	width uint32
}

func (f fabricHealthField) set(mask, value uint32) uint32 {
	return (mask & ^(f.width << f.shift)) | ((value & f.width) << f.shift)
}

func (f fabricHealthField) get(mask uint32) uint32 {
	return (mask >> f.shift) & f.width
}

var (
	fabricHealthDegradedBW            = fabricHealthField{shift: 0, width: 0x3}
	fabricHealthRouteRecovery         = fabricHealthField{shift: 2, width: 0x3}
	fabricHealthRouteUnhealthy        = fabricHealthField{shift: 4, width: 0x3}
	fabricHealthAccessTimeoutRecovery = fabricHealthField{shift: 6, width: 0x3}
	fabricHealthIncorrectConfig       = fabricHealthField{shift: 8, width: 0xf}
)

// Incorrect-configuration values, mirroring
// NVML_GPU_FABRIC_HEALTH_MASK_INCORRECT_CONFIGURATION_*. Unlike the boolean
// conditions this slot carries which misconfiguration was detected, so it is
// configured by name rather than as a bool.
const fabricIncorrectConfigNone uint32 = 1

var fabricIncorrectConfigValues = map[string]uint32{
	"not_supported":        0,
	"none":                 fabricIncorrectConfigNone,
	"incorrect_sysguid":    2,
	"incorrect_chassis_sn": 3,
	"no_partition":         4,
	"insufficient_nvlinks": 5,
	"incompatible_gpu_fw":  6,
	"invalid_location":     7,
	"gpu_state_invalid":    8,
}

// FabricIncorrectConfigNames lists the accepted fabric.health
// incorrect_configuration values, so the CLI and its help text cannot drift
// from what the engine parses.
func FabricIncorrectConfigNames() []string {
	out := make([]string, 0, len(fabricIncorrectConfigValues))
	for name := range fabricIncorrectConfigValues {
		out = append(out, name)
	}
	return out
}

// resolveFabricHealthMask builds the health mask a consumer decodes. A raw
// health_mask is an escape hatch for encodings the symbolic keys cannot
// express (a partition-assigned answer, a future condition) and wins outright;
// otherwise every condition is encoded from fabric.health, which defaults to
// an all-clear fabric.
func resolveFabricHealthMask(cfg *FabricConfig) uint32 {
	if cfg.HealthMask != nil {
		return *cfg.HealthMask
	}
	h := cfg.Health
	if h == nil {
		h = &FabricHealthConfig{}
	}
	var mask uint32
	mask = fabricHealthDegradedBW.set(mask, fabricHealthBool(h.DegradedBandwidth))
	mask = fabricHealthRouteRecovery.set(mask, fabricHealthBool(h.RouteRecovery))
	mask = fabricHealthRouteUnhealthy.set(mask, fabricHealthBool(h.RouteUnhealthy))
	mask = fabricHealthAccessTimeoutRecovery.set(mask, fabricHealthBool(h.AccessTimeoutRecovery))
	mask = fabricHealthIncorrectConfig.set(mask, parseFabricIncorrectConfig(h.IncorrectConfiguration))
	return mask
}

func fabricHealthBool(faulted bool) uint32 {
	if faulted {
		return fabricHealthTrue
	}
	return fabricHealthFalse
}

// parseFabricIncorrectConfig maps a configured name to its mask value. An
// empty or unrecognised name means "no misconfiguration detected", keeping
// misconfiguration from blocking startup (as parseFabricState does).
func parseFabricIncorrectConfig(s string) uint32 {
	if v, ok := fabricIncorrectConfigValues[strings.ToLower(strings.TrimSpace(s))]; ok {
		return v
	}
	return fabricIncorrectConfigNone
}

// resolveFabricHealthSummary decides the overall summary. An explicitly
// configured summary always wins so a profile can pin an inconsistent pair
// (e.g. a driver that reports Unhealthy without saying why); otherwise the
// summary is derived from the mask, which keeps it honest whether the mask
// came from the symbolic keys or from the raw escape hatch.
func resolveFabricHealthSummary(cfg *FabricConfig, mask uint32) uint8 {
	if summary, ok := parseFabricHealthSummary(cfg.HealthSummary); ok {
		return summary
	}
	return deriveFabricHealthSummary(mask)
}

// parseFabricHealthSummary reports the pinned summary and whether one was
// pinned at all. "" and "auto" both mean "derive from the conditions"; an
// unrecognised value is treated the same way rather than failing the device.
func parseFabricHealthSummary(s string) (uint8, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "healthy":
		return FabricHealthSummaryHealthy, true
	case "unhealthy":
		return FabricHealthSummaryUnhealthy, true
	case "limited_capacity", "limitedcapacity":
		return FabricHealthSummaryLimitedCapacity, true
	case "not_supported", "notsupported":
		return FabricHealthSummaryNotSupported, true
	default:
		return 0, false
	}
}

// deriveFabricHealthSummary summarises a health mask the way a fabric manager
// does: any routing or configuration fault makes the attachment unhealthy,
// degraded bandwidth on its own only limits capacity, and a mask that answers
// nothing at all reports nothing at all.
func deriveFabricHealthSummary(mask uint32) uint8 {
	if mask == 0 {
		return FabricHealthSummaryNotSupported
	}
	faulted := fabricHealthRouteRecovery.get(mask) == fabricHealthTrue ||
		fabricHealthRouteUnhealthy.get(mask) == fabricHealthTrue ||
		fabricHealthAccessTimeoutRecovery.get(mask) == fabricHealthTrue ||
		fabricHealthIncorrectConfig.get(mask) > fabricIncorrectConfigNone
	switch {
	case faulted:
		return FabricHealthSummaryUnhealthy
	case fabricHealthDegradedBW.get(mask) == fabricHealthTrue:
		return FabricHealthSummaryLimitedCapacity
	default:
		return FabricHealthSummaryHealthy
	}
}
