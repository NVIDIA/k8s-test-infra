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
	"encoding/hex"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// FabricState* values mirror NVML_GPU_FABRIC_STATE_* from the public NVML
// header. We re-declare them here so the engine layer never has to pull
// in CGo and so that callers (and tests) can compare against named
// constants instead of magic numbers.
const (
	FabricStateNotSupported uint8 = 0
	FabricStateNotStarted   uint8 = 1
	FabricStateInProgress   uint8 = 2
	FabricStateCompleted    uint8 = 3
)

// FabricInfo is the engine-internal shape of a GPU's fabric attributes
// (the union of v1, v2, v3 NVML structs). The CGo bridge converts this
// to the appropriate C struct version selected by the caller.
//
// No PartitionID: nvml_types.h does not carry a partition_id field on
// any of v1/v2/v3, so plumbing one through the engine would only be
// dead weight. Reintroduce when there is a real NVML struct version
// that exposes it.
type FabricInfo struct {
	ClusterUUID   [16]byte
	Status        uint32 // NVML return code embedded inside the struct (SUCCESS by default)
	CliqueID      uint32
	State         uint8
	HealthMask    uint32
	HealthSummary uint8
}

// GetMockFabricInfo returns the v1 fabric information for this device.
// Returns ERROR_NOT_SUPPORTED when no FabricConfig is attached, matching
// real NVML behaviour on non-fabric-attached GPUs.
//
// Named to avoid shadowing the embedded dgxa100.Device's interface method
// `GetGpuFabricInfo() (nvml.GpuFabricInfo, nvml.Return)` while still
// giving the bridge a single typed entry point.
func (d *ConfigurableDevice) GetMockFabricInfo() (FabricInfo, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return FabricInfo{}, ret
	}
	if d.config == nil || d.config.Fabric == nil {
		return FabricInfo{}, nvml.ERROR_NOT_SUPPORTED
	}
	info := buildFabricInfo(d.config.Fabric)
	debugLog("[NVML] nvmlDeviceGetGpuFabricInfo -> clique=%d state=%d\n", info.CliqueID, info.State)
	return info, nvml.SUCCESS
}

// GetMockFabricInfoV returns the versioned (v2/v3) fabric information
// for this device. The struct version selection happens in the bridge —
// the engine returns every field and lets the bridge zero out what the
// caller's selected version cannot represent.
//
// Named to avoid shadowing the embedded dgxa100.Device's interface
// method `GetGpuFabricInfoV() GpuFabricInfoHandler`.
func (d *ConfigurableDevice) GetMockFabricInfoV() (FabricInfo, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return FabricInfo{}, ret
	}
	if d.config == nil || d.config.Fabric == nil {
		return FabricInfo{}, nvml.ERROR_NOT_SUPPORTED
	}
	info := buildFabricInfo(d.config.Fabric)
	debugLog("[NVML] nvmlDeviceGetGpuFabricInfoV -> clique=%d state=%d healthMask=0x%x\n",
		info.CliqueID, info.State, info.HealthMask)
	return info, nvml.SUCCESS
}

func buildFabricInfo(cfg *FabricConfig) FabricInfo {
	info := FabricInfo{
		CliqueID:   cfg.CliqueID,
		HealthMask: cfg.HealthMask,
		State:      resolveFabricState(cfg.State),
	}
	info.ClusterUUID = parseClusterUUID(cfg.ClusterUUID)
	return info
}

// resolveFabricState maps the configured state string to an NVML state.
// The special value "auto" couples the state to the fake fabricmanager's
// readiness marker (decision D-a); every other value is a static mapping.
func resolveFabricState(s string) uint8 {
	if strings.EqualFold(strings.TrimSpace(s), "auto") {
		return fabricReadiness.state()
	}
	return parseFabricState(s)
}

func parseFabricState(s string) uint8 {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "completed":
		// Empty defaults to "completed" — the most useful steady state
		// for the happy-path tests this mock exists to enable.
		return FabricStateCompleted
	case "not_started", "notstarted":
		return FabricStateNotStarted
	case "in_progress", "inprogress":
		return FabricStateInProgress
	case "not_supported", "notsupported":
		return FabricStateNotSupported
	default:
		return FabricStateCompleted
	}
}

// parseClusterUUID accepts either a bare 32-hex string or an RFC-4122
// style dashed UUID and packs it into a 16-byte buffer. Non-hex
// characters are silently dropped; short input is right-padded with
// zeros so misconfiguration does not block startup.
func parseClusterUUID(s string) [16]byte {
	var out [16]byte
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			return r
		default:
			return -1
		}
	}, s)
	if len(cleaned) > 32 {
		cleaned = cleaned[:32]
	}
	for len(cleaned) < 32 {
		cleaned += "0"
	}
	b, err := hex.DecodeString(cleaned)
	if err != nil {
		return out
	}
	copy(out[:], b)
	return out
}
