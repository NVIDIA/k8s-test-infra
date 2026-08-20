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
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// PeerType* are the platform-indicated NVLink peer types NVML reports in
// nvmlPlatformInfo_t.peerType ("switch present or not"), which `nvidia-smi -q`
// renders as "Direct Connected" / "Switch Connected". The numeric encoding is
// not published in nvml.h; it is pinned here by the rendering an NVL72 profile
// produces, where a GPU reaching its peers through an NVSwitch tray is
// "Switch Connected".
const (
	PeerTypeDirectConnected uint8 = 0
	PeerTypeSwitchConnected uint8 = 1
)

// chassisSerialLen is the width of nvmlPlatformInfo_t.chassisSerialNumber.
// NVML documents Blackwell as filling only the first 13 bytes, so the buffer
// always carries a NUL terminator.
const chassisSerialLen = 16

// PlatformInfo is the engine-internal shape of a GPU's platform identity —
// where the board physically sits in a rack. The CGo bridge converts it to
// whichever nvmlPlatformInfo_t struct version the caller selected.
//
// Only ModuleID is per-GPU: NVML defines it as "ID of this GPU within the
// node", while the chassis serial, slot, tray, and host id describe the one
// node this process runs on and are therefore shared by all of its GPUs.
// No IbGuid: nvidia-smi sources "GPU Fabric GUID" from nvmlDeviceGetPdi, so
// carrying an unread GUID through the engine would only be dead weight.
type PlatformInfo struct {
	ChassisSerialNumber [chassisSerialLen]byte
	SlotNumber          uint8
	TrayIndex           uint8
	HostID              uint8
	PeerType            uint8
	ModuleID            uint8
}

// chassisSerial reads the serial back as the string nvidia-smi renders.
func (p PlatformInfo) chassisSerial() string {
	if i := strings.IndexByte(string(p.ChassisSerialNumber[:]), 0); i >= 0 {
		return string(p.ChassisSerialNumber[:i])
	}
	return string(p.ChassisSerialNumber[:])
}

// GetMockPlatformInfo returns this GPU's platform identity, backing
// nvmlDeviceGetPlatformInfo and the "Platform Info" block of `nvidia-smi -q`.
//
// A device with no platform block yields ERROR_NOT_SUPPORTED, which
// nvidia-smi renders as N/A — the correct reading on any board whose platform
// cannot report a physical location, i.e. everything outside a Grace-Blackwell
// rack. Only the NVL72 profiles declare one.
//
// Named GetMockPlatformInfo to avoid shadowing the embedded dgxa100.Device's
// GetPlatformInfo method, following GetMockFabricInfo.
func (d *ConfigurableDevice) GetMockPlatformInfo() (PlatformInfo, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return PlatformInfo{}, ret
	}
	cfg := d.cfg()
	if cfg.Platform == nil {
		return PlatformInfo{}, nvml.ERROR_NOT_SUPPORTED
	}
	info := buildPlatformInfo(cfg.Platform)
	debugLog("[NVML] nvmlDeviceGetPlatformInfo -> slot=%d tray=%d host=%d module=%d\n",
		info.SlotNumber, info.TrayIndex, info.HostID, info.ModuleID)
	return info, nvml.SUCCESS
}

// GetMockModuleID returns the GPU's module id within its node, backing
// nvmlDeviceGetModuleId. It answers from the same platform block as
// GetMockPlatformInfo so the two APIs cannot disagree about one GPU.
func (d *ConfigurableDevice) GetMockModuleID() (uint32, nvml.Return) {
	info, ret := d.GetMockPlatformInfo()
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	return uint32(info.ModuleID), nvml.SUCCESS
}

func buildPlatformInfo(cfg *PlatformConfig) PlatformInfo {
	info := PlatformInfo{
		SlotNumber: cfg.SlotNumber,
		TrayIndex:  cfg.TrayIndex,
		HostID:     cfg.HostID,
		PeerType:   parsePeerType(cfg.PeerType),
		ModuleID:   cfg.ModuleID,
	}
	// One byte short of the buffer: the last byte stays zero so the copy is
	// always NUL-terminated, however long the configured serial is.
	copy(info.ChassisSerialNumber[:chassisSerialLen-1], cfg.ChassisSerialNumber)
	return info
}

// parsePeerType maps the configured peer type onto NVML's encoding. Anything
// unrecognised — including the empty string — is "direct connected", the
// reading of a GPU with no NVSwitch between it and its peers.
func parsePeerType(s string) uint8 {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "switch_connected", "switchconnected", "switch":
		return PeerTypeSwitchConnected
	default:
		return PeerTypeDirectConnected
	}
}
