// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"hash/fnv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// GetNumGpuCores returns the profile's CUDA core count. nvml.h declines the
// query on a MIG-enabled GPU and on a MIG device, pointing the caller at the
// GPU instance profile instead, and the mock does the same.
func (d *ConfigurableDevice) GetNumGpuCores() (int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	if d.mig != nil {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	if _, ret := d.migEnabled(); ret == nvml.SUCCESS {
		debugLog("[NVML] nvmlDeviceGetNumGpuCores -> NOT_SUPPORTED (MIG enabled)\n")
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	cores := d.cfg().NumGPUCores
	debugLog("[NVML] nvmlDeviceGetNumGpuCores -> %d\n", cores)
	if cores <= 0 {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}
	return cores, nvml.SUCCESS
}

// GetAddressingMode reports the profile's addressing_mode. A profile that does
// not declare one answers NOT_SUPPORTED, which nvidia-smi renders as N/A,
// rather than claiming a mode the modelled host may not run.
func (d *ConfigurableDevice) GetAddressingMode() (nvml.DeviceAddressingMode, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.DeviceAddressingMode{}, ret
	}
	configured := d.cfg().AddressingMode
	var mode nvml.DeviceAddressingModeType
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case "":
		return nvml.DeviceAddressingMode{}, nvml.ERROR_NOT_SUPPORTED
	case "none":
		mode = nvml.DEVICE_ADDRESSING_MODE_NONE
	case "hmm":
		mode = nvml.DEVICE_ADDRESSING_MODE_HMM
	case "ats":
		mode = nvml.DEVICE_ADDRESSING_MODE_ATS
	default:
		warnLog("[CONFIG] unrecognized addressing_mode %q; want hmm, ats or none — reporting NOT_SUPPORTED\n", configured)
		return nvml.DeviceAddressingMode{}, nvml.ERROR_NOT_SUPPORTED
	}
	debugLog("[NVML] nvmlDeviceGetAddressingMode -> %d\n", mode)
	return nvml.DeviceAddressingMode{Value: uint32(mode)}, nvml.SUCCESS
}

// GetCapabilities reports the device capability mask. EGM (Extended GPU
// Memory) is the only capability NVML defines. It has to be turned on in the
// kernel module, so it is off unless the profile sets egm: the captured Grace
// boards have it off, the captured Vera Rubin tray on.
func (d *ConfigurableDevice) GetCapabilities() (nvml.DeviceCapabilities, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.DeviceCapabilities{}, ret
	}
	var caps nvml.DeviceCapabilities
	if d.cfg().EGM {
		caps.CapMask = nvml.DEV_CAP_EGM
	}
	debugLog("[NVML] nvmlDeviceGetCapabilities -> capMask=%d\n", caps.CapMask)
	return caps, nvml.SUCCESS
}

// GetPdi returns the Per Device Identifier, a 64-bit ID fused into the GPU.
func (d *ConfigurableDevice) GetPdi() (nvml.Pdi, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.Pdi{}, ret
	}
	pdi := d.pdi(d.cfg())
	debugLog("[NVML] nvmlDeviceGetPdi -> %#016x\n", pdi)
	return nvml.Pdi{Value: pdi}, nvml.SUCCESS
}

// pdi is the configured PDI, or else one derived from the physical GPU's UUID
// so that every process loading the mock agrees on it. A MIG device shares its
// parent's config and UUID, and so reports the PDI of the GPU it is carved from.
func (d *ConfigurableDevice) pdi(cfg *DeviceConfig) uint64 {
	if cfg.PDI != nil {
		return uint64(*cfg.PDI)
	}
	return pdiFromUUID(d.UUID)
}

// pdiFromUUID is the PDI of a GPU that declares none.
func pdiFromUUID(uuid string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(uuid)) // hash.Hash.Write never returns an error
	return h.Sum64()
}

// OnSameBoard reports whether two devices share a physical board: a GPU and its
// MIG devices always do, and two GPUs do when the profile gives them the same
// board_id. The board ID encodes exactly that, as GetMultiGpuBoard reads it.
func (d *ConfigurableDevice) OnSameBoard(other nvml.Device) (int, nvml.Return) {
	peer, ok := other.(*ConfigurableDevice)
	if !ok || peer == nil {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	for _, dev := range []*ConfigurableDevice{d, peer} {
		if ret := dev.handleLookupReturn(); ret != nvml.SUCCESS {
			return 0, ret
		}
	}
	same := 0
	if d.boardID == peer.boardID {
		same = 1
	}
	debugLog("[NVML] nvmlDeviceOnSameBoard(%#x, %#x) -> %d\n", d.boardID, peer.boardID, same)
	return same, nvml.SUCCESS
}

// GetBridgeChipInfo declines: bridge chips sit on multi-GPU boards, nvml.h
// answers NOT_SUPPORTED everywhere else, and no profile describes the bridge
// chips of a board its GPUs share.
func (d *ConfigurableDevice) GetBridgeChipInfo() (nvml.BridgeChipHierarchy, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.BridgeChipHierarchy{}, ret
	}
	return nvml.BridgeChipHierarchy{}, nvml.ERROR_NOT_SUPPORTED
}

// GetIrqNum declines: no profile declares an interrupt line, and a consumer
// that needs one is better served by no answer than by a number that names
// nothing on the host. The simulated sysfs irq file reads 0 for the same reason.
func (d *ConfigurableDevice) GetIrqNum() (int, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return 0, ret
	}
	return 0, nvml.ERROR_NOT_SUPPORTED
}
