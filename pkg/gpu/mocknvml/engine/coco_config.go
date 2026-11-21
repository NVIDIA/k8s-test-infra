// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/gpus"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

const (
	// Values of ConfidentialComputeConfig.Mode.
	cocoModeOff      = "off"
	cocoModeOn       = "on"
	cocoModeDevtools = "devtools"

	// Bounds of the key-rotation attacker advantage, from the NVML header.
	minKeyRotationAttackerAdvantage = 50
	maxKeyRotationAttackerAdvantage = 65
	// defaultKeyRotationAttackerAdvantage is what the driver reports before
	// anyone changes it.
	defaultKeyRotationAttackerAdvantage = 60
)

// ConfidentialComputeConfig is the profile's confidential_compute block. Every
// field is optional; the zero value is CC off.
type ConfidentialComputeConfig struct {
	// Mode is off (default), on or devtools.
	Mode string `json:"mode,omitempty"`
	// MultiGPUMode is none (default), protected_pcie or nvle.
	MultiGPUMode string `json:"multi_gpu_mode,omitempty"`
	// CPUCaps is none (default), amd_sev, intel_tdx, amd_sev_snp or amd_snp_vtom.
	CPUCaps string `json:"cpu_caps,omitempty"`
	// KeyRotationAttackerAdvantage is 50..65; zero means the default.
	KeyRotationAttackerAdvantage uint64 `json:"key_rotation_attacker_advantage,omitzero"`
	// UnprotectedMemKiB is the unprotected size every GPU's memory allocator
	// reports while CC is on; zero leaves the whole framebuffer protected.
	UnprotectedMemKiB uint64 `json:"unprotected_mem_kib,omitzero"`
}

// CoCoSpec is the resolved node-wide CC state. The uint32 fields hold
// the NVML constants so getters copy them straight into the C structs.
type CoCoSpec struct {
	// Capable: every GPU is Hopper or newer.
	Capable bool
	// On: the profile mode is on or devtools.
	On bool
	// DevTools: the profile mode is devtools.
	DevTools     bool
	Environment  uint32
	MultiGPUMode uint32
	CPUCaps      uint32
	Ready        bool
	// Revoked: the GPUs left the ready state on a driver that revokes, and
	// stay unready until the driver reloads.
	Revoked           bool
	AttackerAdvantage uint64
	UnprotectedMemKiB uint64
}

// protected reports whether the GPUs run a protected mode at all: the CC
// feature, or Protected PCIe, which runs with the CC feature off yet reports
// its multi-GPU mode, attests, and gates work on the ready state like a CC
// node does. NVLE is no mode of its own: it is part of the CC feature.
func (spec CoCoSpec) protected() bool {
	return spec.On || spec.MultiGPUMode == nvml.CC_SYSTEM_MULTIGPU_PROTECTED_PCIE
}

// The maps double as the validators: a key missing here is an unknown value.
var (
	cocoMultiGPUModes = map[string]uint32{
		"":               nvml.CC_SYSTEM_MULTIGPU_NONE,
		"none":           nvml.CC_SYSTEM_MULTIGPU_NONE,
		"protected_pcie": nvml.CC_SYSTEM_MULTIGPU_PROTECTED_PCIE,
		"nvle":           nvml.CC_SYSTEM_MULTIGPU_NVLE,
	}
	cocoCPUCaps = map[string]uint32{
		"":             nvml.CC_SYSTEM_CPU_CAPS_NONE,
		"none":         nvml.CC_SYSTEM_CPU_CAPS_NONE,
		"amd_sev":      nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV,
		"intel_tdx":    nvml.CC_SYSTEM_CPU_CAPS_INTEL_TDX,
		"amd_sev_snp":  nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV_SNP,
		"amd_snp_vtom": nvml.CC_SYSTEM_CPU_CAPS_AMD_SNP_VTOM,
	}
)

// resolveCoCo turns the profile block and the node's GPU architectures
// into the state the getters answer from. A nil block is CC off. Validation has
// already refused unknown enum values, so a lookup miss maps to the zero value.
func resolveCoCo(cfg *YAMLConfig, archs []gpuarch.Arch) CoCoSpec {
	spec := CoCoSpec{
		Capable:           len(archs) > 0,
		Environment:       nvml.CC_SYSTEM_ENVIRONMENT_UNAVAILABLE,
		AttackerAdvantage: defaultKeyRotationAttackerAdvantage,
	}
	for _, a := range archs {
		if !a.AtLeast(gpuarch.Hopper) {
			spec.Capable = false
			break
		}
	}

	var c ConfidentialComputeConfig
	if cfg != nil && cfg.ConfidentialCompute != nil {
		c = *cfg.ConfidentialCompute
	}
	spec.On = c.Mode == cocoModeOn || c.Mode == cocoModeDevtools
	spec.DevTools = c.Mode == cocoModeDevtools
	spec.Environment = bootEnvironment(spec)
	spec.MultiGPUMode = cocoMultiGPUModes[c.MultiGPUMode]
	spec.CPUCaps = cocoCPUCaps[c.CPUCaps]
	spec.Ready = bootsReady(spec)
	if c.KeyRotationAttackerAdvantage != 0 {
		spec.AttackerAdvantage = c.KeyRotationAttackerAdvantage
	}
	spec.UnprotectedMemKiB = c.UnprotectedMemKiB
	return spec
}

// bootEnvironment is the CC environment the driver reports, which follows the
// mode rather than the operator: unavailable until the CC feature is on, then
// production, or simulation in DevTools mode. Pre-Hopper hardware, which
// cannot turn CC on, stays unavailable.
func bootEnvironment(spec CoCoSpec) uint32 {
	switch {
	case !spec.Capable || !spec.On:
		return nvml.CC_SYSTEM_ENVIRONMENT_UNAVAILABLE
	case spec.DevTools:
		return nvml.CC_SYSTEM_ENVIRONMENT_SIM
	default:
		return nvml.CC_SYSTEM_ENVIRONMENT_PROD
	}
}

// bootsReady is the ready state a node comes up in, which the driver decides
// rather than the operator. DevTools mode accepts work from boot, except with
// NVLE, whose multi-GPU setup has to finish first. Every other mode starts not
// ready so that a tenant cannot skip attestation; with CC off nothing is ever
// ready. nvmlSystemSetConfComputeGpusReadyState moves it from there.
func bootsReady(spec CoCoSpec) bool {
	return spec.DevTools && spec.MultiGPUMode != nvml.CC_SYSTEM_MULTIGPU_NVLE
}

// validateCoCo refuses a confidential_compute block the simulated
// hardware could not have: unknown enums, an out-of-range advantage, CC on a
// pre-Hopper GPU, Protected PCIe outside Hopper or alongside CC, NVLE below
// Blackwell, or an unprotected size beyond a GPU's framebuffer. Each GPU is
// checked through its merged config so per-device overrides of architecture
// or memory are honoured.
func validateCoCo(cfg *YAMLConfig) error {
	c := cfg.ConfidentialCompute
	if c == nil {
		return nil
	}
	if err := validateCoCoValues(c); err != nil {
		return err
	}
	on := c.Mode == cocoModeOn || c.Mode == cocoModeDevtools
	host := &Config{YAMLConfig: cfg}
	for _, dev := range cocoDeviceConfigs(host) {
		if err := validateCoCoDevice(c, on, dev); err != nil {
			return err
		}
	}
	return nil
}

// validateCoCoValues checks the block's own fields, independent of any GPU.
func validateCoCoValues(c *ConfidentialComputeConfig) error {
	const prefix = "confidential_compute."
	switch c.Mode {
	case "", cocoModeOff, cocoModeOn, cocoModeDevtools:
	default:
		return fmt.Errorf("%smode %q must be off, on or devtools", prefix, c.Mode)
	}
	if _, ok := cocoMultiGPUModes[c.MultiGPUMode]; !ok {
		return fmt.Errorf("%smulti_gpu_mode %q must be none, protected_pcie or nvle", prefix, c.MultiGPUMode)
	}
	if _, ok := cocoCPUCaps[c.CPUCaps]; !ok {
		return fmt.Errorf("%scpu_caps %q must be none, amd_sev, intel_tdx, amd_sev_snp or amd_snp_vtom", prefix, c.CPUCaps)
	}
	if adv := c.KeyRotationAttackerAdvantage; adv != 0 &&
		(adv < minKeyRotationAttackerAdvantage || adv > maxKeyRotationAttackerAdvantage) {
		return fmt.Errorf("%skey_rotation_attacker_advantage %d must be %d..%d",
			prefix, adv, minKeyRotationAttackerAdvantage, maxKeyRotationAttackerAdvantage)
	}
	return nil
}

// validateCoCoDevice checks the block against one GPU's merged config.
func validateCoCoDevice(c *ConfidentialComputeConfig, on bool, dev *DeviceConfig) error {
	const prefix = "confidential_compute."
	// An unrecognised name parses as Unknown, which clears no gate.
	arch, _ := gpuarch.Parse(dev.Architecture)
	if on && !arch.AtLeast(gpuarch.Hopper) {
		return fmt.Errorf("%smode %q needs Hopper or newer, got %q", prefix, c.Mode, dev.Architecture)
	}
	if err := validateCoCoMultiGPUMode(c, arch); err != nil {
		return err
	}
	if fb := framebufferKiB(dev); c.UnprotectedMemKiB > fb {
		return fmt.Errorf("%sunprotected_mem_kib %d exceeds the framebuffer of %d KiB",
			prefix, c.UnprotectedMemKiB, fb)
	}
	return nil
}

// inheritedFramebufferBytes is the framebuffer a GPU reports when its config
// declares no memory: that of the base mock device every GPU is built on
// (dgxa100.New), which TestInheritedFramebufferBytes_MatchesTheBaseDevice pins.
var inheritedFramebufferBytes = gpus.A100_SXM4_40GB.MemoryMB << 20

// framebufferKiB is the framebuffer a GPU built from dev reports, in KiB: the
// declared memory, or the base device's when none is declared.
func framebufferKiB(dev *DeviceConfig) uint64 {
	if dev.Memory != nil {
		return dev.Memory.TotalBytes >> 10
	}
	return inheritedFramebufferBytes >> 10
}

func validateCoCoMultiGPUMode(c *ConfidentialComputeConfig, arch gpuarch.Arch) error {
	const prefix = "confidential_compute.multi_gpu_mode"
	switch c.MultiGPUMode {
	case "protected_pcie":
		switch c.Mode {
		case cocoModeOn, cocoModeDevtools:
			return fmt.Errorf("%s protected_pcie requires mode off; CC and Protected PCIe are mutually exclusive", prefix)
		}
		if !arch.Is(gpuarch.Hopper) {
			return fmt.Errorf("%s protected_pcie needs Hopper, got %q", prefix, arch)
		}
	case "nvle":
		// NVLE is a bit of the Blackwell CC register, set with the CC mode;
		// unlike Protected PCIe, nothing switches it on without CC.
		if c.Mode != cocoModeOn && c.Mode != cocoModeDevtools {
			return fmt.Errorf("%s nvle requires mode on or devtools; NVLE is part of the CC feature", prefix)
		}
		if !arch.AtLeast(gpuarch.Blackwell) {
			return fmt.Errorf("%s nvle needs Blackwell or newer, got %q", prefix, arch)
		}
	}
	return nil
}

// cocoDeviceConfigs returns the merged config of every GPU the engine
// creates from the profile, so a GPU that falls back to the defaults is checked
// whatever the override list looks like.
func cocoDeviceConfigs(c *Config) []*DeviceConfig {
	n := yamlDeviceCount(c.YAMLConfig)
	out := make([]*DeviceConfig, 0, n)
	for i := range n {
		out = append(out, c.GetDeviceConfig(i))
	}
	return out
}
