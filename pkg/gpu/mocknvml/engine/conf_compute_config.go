// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
)

const (
	// Values of ConfidentialComputeConfig.Mode.
	confComputeModeOff      = "off"
	confComputeModeOn       = "on"
	confComputeModeDevtools = "devtools"

	// Bounds of the key-rotation attacker advantage, from the NVML header.
	minKeyRotationAttackerAdvantage = 50
	maxKeyRotationAttackerAdvantage = 65
	// defaultKeyRotationAttackerAdvantage is what the driver reports before
	// anyone changes it.
	defaultKeyRotationAttackerAdvantage = 60
)

// ConfidentialComputeConfig is the profile's confidential_compute block. Every
// field is optional; the zero value is CC off on a production environment.
type ConfidentialComputeConfig struct {
	// Mode is off (default), on or devtools.
	Mode string `json:"mode,omitempty"`
	// Environment is prod (default) or sim.
	Environment string `json:"environment,omitempty"`
	// MultiGPUMode is none (default), protected_pcie or nvle.
	MultiGPUMode string `json:"multi_gpu_mode,omitempty"`
	// CPUCaps is none (default), amd_sev, intel_tdx, amd_sev_snp or amd_snp_vtom.
	CPUCaps string `json:"cpu_caps,omitempty"`
	// Ready is the initial GPUs ready state.
	Ready bool `json:"ready,omitzero"`
	// KeyRotationAttackerAdvantage is 50..65; zero means the default.
	KeyRotationAttackerAdvantage uint64 `json:"key_rotation_attacker_advantage,omitzero"`
	// UnprotectedMemKiB is the initial unprotected size per GPU.
	UnprotectedMemKiB uint64 `json:"unprotected_mem_kib,omitzero"`
}

// ConfComputeSpec is the resolved node-wide CC state. The uint32 fields hold
// the NVML constants so getters copy them straight into the C structs.
type ConfComputeSpec struct {
	// Capable: every GPU is Hopper or newer.
	Capable bool
	// On: the profile mode is on or devtools.
	On bool
	// DevTools: the profile mode is devtools.
	DevTools          bool
	Environment       uint32
	MultiGPUMode      uint32
	CPUCaps           uint32
	Ready             bool
	AttackerAdvantage uint64
	UnprotectedMemKiB uint64
}

// protected reports whether the GPUs run a protected mode at all: the CC
// feature, or multi-GPU protection on its own. Protected PCIe nodes run with
// the CC feature off, yet report their multi-GPU mode and gate work on the
// ready state like a CC node does.
func (spec ConfComputeSpec) protected() bool {
	return spec.On || spec.MultiGPUMode != nvml.CC_SYSTEM_MULTIGPU_NONE
}

// The maps double as the validators: a key missing here is an unknown value.
var (
	confComputeEnvironments = map[string]uint32{
		"":     nvml.CC_SYSTEM_ENVIRONMENT_PROD,
		"prod": nvml.CC_SYSTEM_ENVIRONMENT_PROD,
		"sim":  nvml.CC_SYSTEM_ENVIRONMENT_SIM,
	}
	confComputeMultiGPUModes = map[string]uint32{
		"":               nvml.CC_SYSTEM_MULTIGPU_NONE,
		"none":           nvml.CC_SYSTEM_MULTIGPU_NONE,
		"protected_pcie": nvml.CC_SYSTEM_MULTIGPU_PROTECTED_PCIE,
		"nvle":           nvml.CC_SYSTEM_MULTIGPU_NVLE,
	}
	confComputeCPUCaps = map[string]uint32{
		"":             nvml.CC_SYSTEM_CPU_CAPS_NONE,
		"none":         nvml.CC_SYSTEM_CPU_CAPS_NONE,
		"amd_sev":      nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV,
		"intel_tdx":    nvml.CC_SYSTEM_CPU_CAPS_INTEL_TDX,
		"amd_sev_snp":  nvml.CC_SYSTEM_CPU_CAPS_AMD_SEV_SNP,
		"amd_snp_vtom": nvml.CC_SYSTEM_CPU_CAPS_AMD_SNP_VTOM,
	}
)

// resolveConfCompute turns the profile block and the node's GPU architectures
// into the state the getters answer from. A nil block is CC off. Validation has
// already refused unknown enum values, so a lookup miss maps to the zero value.
func resolveConfCompute(cfg *YAMLConfig, archs []gpuarch.Arch) ConfComputeSpec {
	spec := ConfComputeSpec{
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
	// Pre-Hopper hardware has no CC environment to report, whatever the
	// profile says.
	if spec.Capable {
		spec.Environment = confComputeEnvironments[c.Environment]
	}
	spec.On = c.Mode == confComputeModeOn || c.Mode == confComputeModeDevtools
	spec.DevTools = c.Mode == confComputeModeDevtools
	spec.MultiGPUMode = confComputeMultiGPUModes[c.MultiGPUMode]
	spec.CPUCaps = confComputeCPUCaps[c.CPUCaps]
	spec.Ready = c.Ready
	if c.KeyRotationAttackerAdvantage != 0 {
		spec.AttackerAdvantage = c.KeyRotationAttackerAdvantage
	}
	spec.UnprotectedMemKiB = c.UnprotectedMemKiB
	return spec
}

// validateConfCompute refuses a confidential_compute block the simulated
// hardware could not have: unknown enums, an out-of-range advantage, CC on a
// pre-Hopper GPU, Protected PCIe outside Hopper or alongside CC, NVLE below
// Blackwell, or an unprotected size beyond a GPU's framebuffer. Each GPU is
// checked through its merged config so per-device overrides of architecture
// or memory are honoured.
func validateConfCompute(cfg *YAMLConfig) error {
	c := cfg.ConfidentialCompute
	if c == nil {
		return nil
	}
	if err := validateConfComputeValues(c); err != nil {
		return err
	}
	on := c.Mode == confComputeModeOn || c.Mode == confComputeModeDevtools
	host := &Config{YAMLConfig: cfg}
	for _, dev := range confComputeDeviceConfigs(host) {
		if err := validateConfComputeDevice(c, on, dev); err != nil {
			return err
		}
	}
	return nil
}

// validateConfComputeValues checks the block's own fields, independent of any GPU.
func validateConfComputeValues(c *ConfidentialComputeConfig) error {
	const prefix = "confidential_compute."
	switch c.Mode {
	case "", confComputeModeOff, confComputeModeOn, confComputeModeDevtools:
	default:
		return fmt.Errorf("%smode %q must be off, on or devtools", prefix, c.Mode)
	}
	if _, ok := confComputeEnvironments[c.Environment]; !ok {
		return fmt.Errorf("%senvironment %q must be prod or sim", prefix, c.Environment)
	}
	if _, ok := confComputeMultiGPUModes[c.MultiGPUMode]; !ok {
		return fmt.Errorf("%smulti_gpu_mode %q must be none, protected_pcie or nvle", prefix, c.MultiGPUMode)
	}
	if _, ok := confComputeCPUCaps[c.CPUCaps]; !ok {
		return fmt.Errorf("%scpu_caps %q must be none, amd_sev, intel_tdx, amd_sev_snp or amd_snp_vtom", prefix, c.CPUCaps)
	}
	if adv := c.KeyRotationAttackerAdvantage; adv != 0 &&
		(adv < minKeyRotationAttackerAdvantage || adv > maxKeyRotationAttackerAdvantage) {
		return fmt.Errorf("%skey_rotation_attacker_advantage %d must be %d..%d",
			prefix, adv, minKeyRotationAttackerAdvantage, maxKeyRotationAttackerAdvantage)
	}
	return nil
}

// validateConfComputeDevice checks the block against one GPU's merged config.
func validateConfComputeDevice(c *ConfidentialComputeConfig, on bool, dev *DeviceConfig) error {
	const prefix = "confidential_compute."
	// An unrecognised name parses as Unknown, which clears no gate.
	arch, _ := gpuarch.Parse(dev.Architecture)
	if on && !arch.AtLeast(gpuarch.Hopper) {
		return fmt.Errorf("%smode %q needs Hopper or newer, got %q", prefix, c.Mode, dev.Architecture)
	}
	if err := validateConfComputeMultiGPUMode(c, arch); err != nil {
		return err
	}
	if dev.Memory != nil && c.UnprotectedMemKiB > dev.Memory.TotalBytes>>10 {
		return fmt.Errorf("%sunprotected_mem_kib %d exceeds the framebuffer of %d KiB",
			prefix, c.UnprotectedMemKiB, dev.Memory.TotalBytes>>10)
	}
	return nil
}

func validateConfComputeMultiGPUMode(c *ConfidentialComputeConfig, arch gpuarch.Arch) error {
	const prefix = "confidential_compute.multi_gpu_mode"
	switch c.MultiGPUMode {
	case "protected_pcie":
		switch c.Mode {
		case confComputeModeOn, confComputeModeDevtools:
			return fmt.Errorf("%s protected_pcie requires mode off; CC and Protected PCIe are mutually exclusive", prefix)
		}
		if !arch.Is(gpuarch.Hopper) {
			return fmt.Errorf("%s protected_pcie needs Hopper, got %q", prefix, arch)
		}
	case "nvle":
		if !arch.AtLeast(gpuarch.Blackwell) {
			return fmt.Errorf("%s nvle needs Blackwell or newer, got %q", prefix, arch)
		}
	}
	return nil
}

// confComputeDeviceConfigs returns the merged config of every GPU the engine
// creates from the profile, so a GPU that falls back to the defaults is checked
// whatever the override list looks like.
func confComputeDeviceConfigs(c *Config) []*DeviceConfig {
	n := yamlDeviceCount(c.YAMLConfig)
	out := make([]*DeviceConfig, 0, n)
	for i := range n {
		out = append(out, c.GetDeviceConfig(i))
	}
	return out
}
