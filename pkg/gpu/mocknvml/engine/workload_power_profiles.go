// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"sort"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Workload power profiles are the pre-tuned performance/power recipes Blackwell
// exposes through `nvidia-smi power-profiles`. Only the two getters are
// modelled; the requested set comes from config rather than from a consumer
// calling the setters.
const (
	// workloadPowerProfileMaxProfiles is the width of the profile masks, and
	// so the first id that has no bit to occupy.
	workloadPowerProfileMaxProfiles = 255

	// maskBitsPerElem mirrors NVML_255_MASK_BITS_PER_ELEM.
	maskBitsPerElem = 32
)

// WorkloadPowerProfileGetProfilesInfo reports the profiles the device
// advertises. perfProfile is indexed by profile id, not densely packed:
// nvidia-smi takes each profile's name from the set bits of perfProfilesMask
// but reads its priority and conflicts from perfProfile[id]. Packing the
// entries instead renders every profile above the first gap with another
// profile's priority — verified against nvidia-smi 580.65.06 with a
// deliberately non-contiguous set.
func (d *ConfigurableDevice) WorkloadPowerProfileGetProfilesInfo() (nvml.WorkloadPowerProfileProfilesInfo, nvml.Return) {
	var info nvml.WorkloadPowerProfileProfilesInfo
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return info, ret
	}
	cfg := d.workloadProfilesConfig()
	if cfg == nil {
		debugLog("[NVML] nvmlDeviceWorkloadPowerProfileGetProfilesInfo -> NOT_SUPPORTED\n")
		return info, nvml.ERROR_NOT_SUPPORTED
	}

	supported := supportedWorkloadProfiles(cfg)
	info.Version = nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileProfilesInfo_v1{}, 1)
	entryVersion := nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileInfo_v1{}, 1)
	for _, p := range supported {
		setMaskBit(&info.PerfProfilesMask, p.ID)
		info.PerfProfile[p.ID] = nvml.WorkloadPowerProfileInfo{
			Version:         entryVersion,
			ProfileId:       p.ID,
			Priority:        p.Priority,
			ConflictingMask: workloadProfileMask(p.Conflicts),
		}
	}
	debugLog("[NVML] nvmlDeviceWorkloadPowerProfileGetProfilesInfo -> %d profiles\n", len(supported))
	return info, nvml.SUCCESS
}

// WorkloadPowerProfileGetCurrentProfiles reports the supported, requested and
// enforced masks. Requested and enforced differ because asking for mutually
// exclusive profiles is allowed; enforced is what survives arbitration.
func (d *ConfigurableDevice) WorkloadPowerProfileGetCurrentProfiles() (nvml.WorkloadPowerProfileCurrentProfiles, nvml.Return) {
	var current nvml.WorkloadPowerProfileCurrentProfiles
	if ret := d.tickFailure(); ret != nvml.SUCCESS {
		return current, ret
	}
	cfg := d.workloadProfilesConfig()
	if cfg == nil {
		debugLog("[NVML] nvmlDeviceWorkloadPowerProfileGetCurrentProfiles -> NOT_SUPPORTED\n")
		return current, nvml.ERROR_NOT_SUPPORTED
	}

	supported := supportedWorkloadProfiles(cfg)
	requested := requestedWorkloadProfiles(cfg, supported)

	current.Version = nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileCurrentProfiles_v1{}, 1)
	for _, p := range supported {
		setMaskBit(&current.PerfProfilesMask, p.ID)
	}
	current.RequestedProfilesMask = workloadProfileMask(requested)
	current.EnforcedProfilesMask = workloadProfileMask(enforcedWorkloadProfiles(supported, requested))
	debugLog("[NVML] nvmlDeviceWorkloadPowerProfileGetCurrentProfiles -> %d supported, %d requested\n",
		len(supported), len(requested))
	return current, nvml.SUCCESS
}

// workloadProfilesConfig returns the device's workload profile config, or nil
// when the device does not model the feature at all.
func (d *ConfigurableDevice) workloadProfilesConfig() *WorkloadPowerProfilesConfig {
	c := d.cfg()
	if c.Power == nil {
		return nil
	}
	return c.Power.WorkloadProfiles
}

// supportedWorkloadProfiles returns the advertised profiles in ascending id
// order, dropping ids with no bit in a 255-bit mask.
func supportedWorkloadProfiles(cfg *WorkloadPowerProfilesConfig) []WorkloadPowerProfileConfig {
	out := make([]WorkloadPowerProfileConfig, 0, len(cfg.Supported))
	for _, p := range cfg.Supported {
		if p.ID < workloadPowerProfileMaxProfiles {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// requestedWorkloadProfiles narrows the configured request to profiles the
// device actually advertises: a device cannot request a profile it never
// offered, so a typo in a profile YAML drops out here rather than reaching
// nvidia-smi.
func requestedWorkloadProfiles(cfg *WorkloadPowerProfilesConfig, supported []WorkloadPowerProfileConfig) []uint32 {
	advertised := make(map[uint32]struct{}, len(supported))
	for _, p := range supported {
		advertised[p.ID] = struct{}{}
	}
	out := make([]uint32, 0, len(cfg.Requested))
	for _, id := range cfg.Requested {
		if _, ok := advertised[id]; ok {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// enforcedWorkloadProfiles resolves conflicts among the requested profiles.
// NVML orders by priority with the lower value winning, so profiles are
// considered in that order and one is dropped when it conflicts with a profile
// already admitted. Ties break on id, which keeps the answer stable rather than
// depending on config order.
func enforcedWorkloadProfiles(supported []WorkloadPowerProfileConfig, requested []uint32) []uint32 {
	byID := make(map[uint32]WorkloadPowerProfileConfig, len(supported))
	for _, p := range supported {
		byID[p.ID] = p
	}

	candidates := make([]WorkloadPowerProfileConfig, 0, len(requested))
	for _, id := range requested {
		candidates = append(candidates, byID[id])
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ID < candidates[j].ID
	})

	admitted := make([]uint32, 0, len(candidates))
	for _, p := range candidates {
		if !conflictsWithAny(p, admitted, byID) {
			admitted = append(admitted, p.ID)
		}
	}
	sort.Slice(admitted, func(i, j int) bool { return admitted[i] < admitted[j] })
	return admitted
}

// conflictsWithAny reports whether p cannot coexist with anything already
// admitted. Both directions count: a profile naming a conflict is as
// disqualifying as being named by one, so an asymmetric config still arbitrates.
func conflictsWithAny(p WorkloadPowerProfileConfig, admitted []uint32, byID map[uint32]WorkloadPowerProfileConfig) bool {
	for _, id := range admitted {
		for _, c := range p.Conflicts {
			if c == id {
				return true
			}
		}
		for _, c := range byID[id].Conflicts {
			if c == p.ID {
				return true
			}
		}
	}
	return false
}

// workloadProfileMask builds a 255-bit mask from profile ids.
func workloadProfileMask(ids []uint32) nvml.Mask255 {
	var mask nvml.Mask255
	for _, id := range ids {
		setMaskBit(&mask, id)
	}
	return mask
}

// setMaskBit sets the bit for one profile id, ignoring ids beyond the mask's
// width rather than aliasing them onto a valid profile.
func setMaskBit(mask *nvml.Mask255, id uint32) {
	if id >= workloadPowerProfileMaxProfiles {
		return
	}
	mask.Mask[id/maskBitsPerElem] |= 1 << (id % maskBitsPerElem)
}
