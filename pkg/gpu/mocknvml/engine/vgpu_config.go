// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"errors"
	"fmt"
	"math/bits"
	"time"
)

const (
	// vgpuInstanceIDStride spaces vGPU instance IDs per physical GPU: GPU i's
	// slot s answers as (i+1)*stride + s + 1, so an ID decodes back to its GPU
	// with no lookup table and no two GPUs' IDs collide.
	vgpuInstanceIDStride = 1000

	// VirtualizationModeHostVGPU makes a GPU a vGPU host: a hypervisor's
	// physical GPU running the vGPU manager and the declared instances.
	VirtualizationModeHostVGPU = "host_vgpu"
	// VirtualizationModeVGPU makes a GPU the vGPU a VM sees.
	VirtualizationModeVGPU = "vgpu"

	// maxVgpuInstancesPerGPU is bounded by the instance ID scheme, which gives
	// each physical GPU a block of vgpuInstanceIDStride IDs.
	maxVgpuInstancesPerGPU = vgpuInstanceIDStride - 1

	// vgpuHostDriverFieldLength is the width of the version and branch fields
	// in the guest's PCI vendor capability; a longer value cannot be written.
	vgpuHostDriverFieldLength = 10

	// gridLicenseProductMaxLength leaves room for the NUL in the 128-byte
	// productName field of nvmlGridLicensableFeature_t.
	gridLicenseProductMaxLength = 127
)

// Guest returns the guest block when the GPU is a vGPU guest, and nil
// otherwise — including for mode vgpu with no block, which reports the mode
// and nothing else.
func (v *VirtualizationConfig) Guest() *VGPUGuestConfig {
	if v == nil || v.Mode != VirtualizationModeVGPU || v.VGPU == nil {
		return nil
	}
	return v.VGPU.Guest
}

// Licensed reports whether the guest holds its license. A license block with
// no state is licensed: declaring a product is the common case.
func (l *VGPULicenseConfig) Licensed() bool {
	return l != nil && (l.State == "" || l.State == "licensed")
}

// validateVGPUConfig checks the virtualization block for the two vGPU modes.
// Every other mode ignores the block and reports its own virtualization mode
// (passthrough as PASSTHROUGH, none or an unrecognised one as bare metal), so
// the "N/A" host_vgpu_mode every shipped profile carries keeps loading.
func validateVGPUConfig(config *YAMLConfig) error {
	v := config.DeviceDefaults.Virtualization
	if v == nil {
		return nil
	}
	var boardMiB uint64
	if m := config.DeviceDefaults.Memory; m != nil {
		boardMiB = m.TotalBytes >> 20
	}
	var err error
	switch v.Mode {
	case VirtualizationModeHostVGPU:
		err = validateVGPUHost(v, boardMiB)
	case VirtualizationModeVGPU:
		err = validateVGPUGuest(v.VGPU)
	}
	if err != nil {
		return fmt.Errorf("device_defaults.virtualization.%w", err)
	}
	return nil
}

func validateVGPUHost(v *VirtualizationConfig, boardMiB uint64) error {
	if v.HostVGPUMode != "sriov" && v.HostVGPUMode != "non_sriov" {
		return fmt.Errorf("host_vgpu_mode: %q is not one of sriov, non_sriov", v.HostVGPUMode)
	}
	if v.VGPU == nil || len(v.VGPU.Types) == 0 {
		return errors.New("vgpu.types: mode host_vgpu needs at least one vGPU type")
	}
	if v.VGPU.Guest != nil {
		return errors.New("vgpu.guest: only valid under mode vgpu")
	}
	types, err := validateVGPUTypes(v.VGPU.Types)
	if err != nil {
		return err
	}
	return validateVGPUInstances(v.VGPU.Instances, types, boardMiB)
}

func validateVGPUTypes(types []VGPUTypeConfig) (map[string]VGPUTypeConfig, error) {
	byName := make(map[string]VGPUTypeConfig, len(types))
	ids := make(map[uint32]bool, len(types))
	for i, t := range types {
		switch {
		case t.ID == 0:
			return nil, fmt.Errorf("vgpu.types[%d].id is required", i)
		case t.Name == "":
			return nil, fmt.Errorf("vgpu.types[%d].name is required", i)
		case ids[t.ID]:
			return nil, fmt.Errorf("vgpu.types[%d].id: %d is declared twice", i, t.ID)
		case byName[t.Name].Name != "":
			return nil, fmt.Errorf("vgpu.types[%d].name: %q is declared twice", i, t.Name)
		case t.FramebufferMiB == 0:
			return nil, fmt.Errorf("vgpu.types[%d].framebuffer_mib is required", i)
		case t.MaxInstances < 1 || t.MaxInstances > maxVgpuInstancesPerGPU:
			return nil, fmt.Errorf("vgpu.types[%d].max_instances: %d is outside 1..%d", i, t.MaxInstances, maxVgpuInstancesPerGPU)
		}
		ids[t.ID] = true
		byName[t.Name] = t
	}
	return byName, nil
}

// validateVGPUInstances checks the running instances. Without heterogeneous
// mode a GPU runs one vGPU type at a time, which is also what makes
// max_instances a per-GPU bound on the whole layout, and the framebuffer in use
// the instance count times that type's framebuffer.
func validateVGPUInstances(instances []VGPUInstanceConfig, types map[string]VGPUTypeConfig, boardMiB uint64) error {
	var (
		first    string
		count    uint64
		declared VGPUTypeConfig
	)
	for i, inst := range instances {
		t, ok := types[inst.Type]
		if !ok {
			return fmt.Errorf("vgpu.instances[%d].type: %q is not a declared vGPU type", i, inst.Type)
		}
		if inst.Count < 0 {
			return fmt.Errorf("vgpu.instances[%d].count: %d is negative", i, inst.Count)
		}
		if first == "" {
			first, declared = inst.Type, t
		} else if inst.Type != first {
			return fmt.Errorf("vgpu.instances[%d].type: %q differs from %q; a GPU runs one vGPU type at a time", i, inst.Type, first)
		}
		// Checked per entry so the running total stays at most max_instances
		// (999) before each addition and the sum cannot wrap.
		count += uint64(max(inst.Count, 1))
		if count > uint64(declared.MaxInstances) {
			return fmt.Errorf("vgpu.instances: %d instances of %q exceed its max_instances %d", count, first, declared.MaxInstances)
		}
	}
	if boardMiB == 0 {
		return nil
	}
	hi, fbMiB := bits.Mul64(count, declared.FramebufferMiB)
	if hi != 0 || fbMiB > boardMiB {
		return fmt.Errorf("vgpu.instances: %d instances of %d MiB exceed the board's %d MiB of framebuffer", count, declared.FramebufferMiB, boardMiB)
	}
	return nil
}

// validateVGPUGuest checks the guest block when one is declared.
func validateVGPUGuest(v *VGPUConfig) error {
	if v == nil {
		return nil
	}
	if len(v.Types) > 0 {
		return errors.New("vgpu.types: only valid under mode host_vgpu")
	}
	if len(v.Instances) > 0 {
		return errors.New("vgpu.instances: only valid under mode host_vgpu")
	}
	g := v.Guest
	if g == nil {
		return nil
	}
	for _, f := range []struct{ name, value string }{
		{"host_driver_version", g.HostDriverVersion},
		{"host_driver_branch", g.HostDriverBranch},
	} {
		if f.value == "" {
			return fmt.Errorf("vgpu.guest.%s is required", f.name)
		}
		if len(f.value) > vgpuHostDriverFieldLength {
			return fmt.Errorf("vgpu.guest.%s: %q is longer than %d bytes", f.name, f.value, vgpuHostDriverFieldLength)
		}
	}
	return validateVGPULicense(g.License)
}

func validateVGPULicense(l *VGPULicenseConfig) error {
	if l == nil {
		return nil
	}
	if l.State != "" && l.State != "licensed" && l.State != "unlicensed" {
		return fmt.Errorf("vgpu.guest.license.state: %q is not one of licensed, unlicensed", l.State)
	}
	if len(l.Product) > gridLicenseProductMaxLength {
		return fmt.Errorf("vgpu.guest.license.product is longer than %d bytes", gridLicenseProductMaxLength)
	}
	if l.Expiry != "" {
		if _, err := time.Parse(time.RFC3339, l.Expiry); err != nil {
			return fmt.Errorf("vgpu.guest.license.expiry: %q is not an RFC 3339 time", l.Expiry)
		}
	}
	return nil
}
