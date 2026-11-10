// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"crypto/sha256"
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// vGPU host simulation.
//
// A GPU in host_vgpu mode runs the instances its current config declares; no
// NVML call creates or destroys them. vGPU types and instances are
// plain IDs in NVML, so nothing here registers handles: a type ID names the
// same type on every GPU that declares it, and an instance ID encodes its GPU.
//
// Every getter answers from static config and none advances the
// failure-injection counter: nvidia-smi calls them once per GPU or instance on
// every run, and ticking would trip an "after_calls" scenario on a schedule
// set by which nvidia-smi view the caller asked for. A lost GPU still
// surfaces.

const (
	// Defaults for the optional type fields, as a compute (C-series) type
	// reports them.
	defaultVgpuClass            = "Compute"
	defaultVgpuInstancesPerVM   = 1
	defaultVgpuDisplayHeads     = 1
	defaultVgpuResolutionWidth  = 4096
	defaultVgpuResolutionHeight = 2160

	// vgpuNotMIGBacked is NVML's answer for the GPU instance profile and the
	// GPU instance of a time-sliced vGPU, which no MIG instance backs.
	vgpuNotMIGBacked = 0xFFFFFFFF
)

// VgpuType is one vGPU type as NVML reports it, optional fields resolved.
type VgpuType struct {
	ID                   uint32
	Name                 string
	Class                string
	License              string
	FramebufferBytes     uint64
	DeviceID             uint64
	SubsystemID          uint64
	MaxInstances         uint32
	MaxInstancesPerVM    uint32
	NumDisplayHeads      uint32
	ResolutionWidth      uint32
	ResolutionHeight     uint32
	FrameRateLimit       uint32
	GPUInstanceProfileID uint32
}

// VgpuInstance is one running vGPU.
type VgpuInstance struct {
	ID   uint32
	Type VgpuType
	UUID string
	// VMID is the UUID of the VM the instance is assigned to. Every instance
	// gets a VM of its own, which is what max_instances_per_vm 1 implies.
	VMID          string
	ECCMode       nvml.EnableState
	GPUInstanceID uint32
}

// VMDriverVersion is the driver version the guest reports. NVML returns this
// string until a driver loads in the VM, and no simulated VM ever boots one.
func (VgpuInstance) VMDriverVersion() string {
	return "Not Available"
}

// LicenseInfo reports the instance's license. A running instance is taken to
// be licensed: an unlicensed guest is the guest-side simulation's business.
func (VgpuInstance) LicenseInfo() nvml.VgpuLicenseInfo {
	return nvml.VgpuLicenseInfo{
		IsLicensed:    1,
		LicenseExpiry: nvml.VgpuLicenseExpiry{Status: nvml.GRID_LICENSE_EXPIRY_PERMANENT},
		CurrentState:  nvml.GRID_LICENSE_STATE_LICENSED,
	}
}

// IsVgpuHost reports whether the GPU runs the vGPU manager. It agrees with
// the vGPU getters, so a GPU that claims the mode with no vGPU block, as a
// runtime override can leave it, is not one.
func (d *ConfigurableDevice) IsVgpuHost() bool {
	return isVgpuHost(d.cfg().Virtualization)
}

func isVgpuHost(v *VirtualizationConfig) bool {
	return v != nil && v.Mode == VirtualizationModeHostVGPU && v.VGPU != nil
}

// vgpuHost returns the host block, or NOT_SUPPORTED for a GPU without the
// vGPU manager, which is how a bare-metal driver answers.
func (d *ConfigurableDevice) vgpuHost() (*VirtualizationConfig, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nil, ret
	}
	v := d.cfg().Virtualization
	if !isVgpuHost(v) {
		return nil, nvml.ERROR_NOT_SUPPORTED
	}
	return v, nvml.SUCCESS
}

// GetHostVgpuMode overrides the embedded mock.
func (d *ConfigurableDevice) GetHostVgpuMode() (nvml.HostVgpuMode, nvml.Return) {
	v, ret := d.vgpuHost()
	if ret != nvml.SUCCESS {
		return 0, ret
	}
	mode := nvml.HOST_VGPU_MODE_NON_SRIOV
	if v.HostVGPUMode == "sriov" {
		mode = nvml.HOST_VGPU_MODE_SRIOV
	}
	debugLog("[NVML] nvmlDeviceGetHostVgpuMode -> %d\n", mode)
	return mode, nvml.SUCCESS
}

// VgpuSupportedTypeIDs lists the declared types in declaration order.
func (d *ConfigurableDevice) VgpuSupportedTypeIDs() ([]uint32, nvml.Return) {
	v, ret := d.vgpuHost()
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	return supportedTypeIDs(v.VGPU), nvml.SUCCESS
}

func supportedTypeIDs(v *VGPUConfig) []uint32 {
	ids := make([]uint32, 0, len(v.Types))
	for _, t := range v.Types {
		ids = append(ids, t.ID)
	}
	return ids
}

// VgpuCreatableTypeIDs lists what could still be created. Without
// heterogeneous mode a GPU runs one type at a time, so once an instance is
// running only its type is creatable, and only until the GPU is full.
func (d *ConfigurableDevice) VgpuCreatableTypeIDs() ([]uint32, nvml.Return) {
	v, ret := d.vgpuHost()
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	active := d.vgpuInstances(v.VGPU)
	if len(active) == 0 {
		return supportedTypeIDs(v.VGPU), nvml.SUCCESS
	}
	t := active[0].Type
	if len(active) >= int(t.MaxInstances) {
		return []uint32{}, nvml.SUCCESS
	}
	return []uint32{t.ID}, nvml.SUCCESS
}

// VgpuActiveInstanceIDs lists the running instances.
func (d *ConfigurableDevice) VgpuActiveInstanceIDs() ([]uint32, nvml.Return) {
	v, ret := d.vgpuHost()
	if ret != nvml.SUCCESS {
		return nil, ret
	}
	instances := d.vgpuInstances(v.VGPU)
	ids := make([]uint32, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	return ids, nvml.SUCCESS
}

// VgpuType resolves a type this GPU declares.
func (d *ConfigurableDevice) VgpuType(id uint32) (VgpuType, nvml.Return) {
	v, ret := d.vgpuHost()
	if ret != nvml.SUCCESS {
		return VgpuType{}, ret
	}
	for _, t := range v.VGPU.Types {
		if t.ID == id {
			return d.resolveVgpuType(t), nvml.SUCCESS
		}
	}
	return VgpuType{}, nvml.ERROR_INVALID_ARGUMENT
}

func (d *ConfigurableDevice) resolveVgpuType(t VGPUTypeConfig) VgpuType {
	out := VgpuType{
		ID:                   t.ID,
		Name:                 t.Name,
		Class:                t.Class,
		License:              t.License,
		FramebufferBytes:     t.FramebufferMiB << 20,
		MaxInstances:         t.MaxInstances,
		MaxInstancesPerVM:    t.MaxInstancesPerVM,
		NumDisplayHeads:      t.NumDisplayHeads,
		ResolutionWidth:      defaultVgpuResolutionWidth,
		ResolutionHeight:     defaultVgpuResolutionHeight,
		FrameRateLimit:       t.FrameRateLimit,
		GPUInstanceProfileID: vgpuNotMIGBacked,
	}
	if out.Class == "" {
		out.Class = defaultVgpuClass
	}
	if out.MaxInstancesPerVM == 0 {
		out.MaxInstancesPerVM = defaultVgpuInstancesPerVM
	}
	if out.NumDisplayHeads == 0 {
		out.NumDisplayHeads = defaultVgpuDisplayHeads
	}
	if t.Resolution != nil {
		out.ResolutionWidth, out.ResolutionHeight = t.Resolution.Width, t.Resolution.Height
	}
	if t.GPUInstanceProfileID != nil {
		out.GPUInstanceProfileID = *t.GPUInstanceProfileID
	}
	if pci := d.cfg().PCI; pci != nil {
		out.DeviceID, out.SubsystemID = uint64(pci.DeviceID), uint64(pci.SubsystemID)
	}
	if t.DeviceID != 0 {
		out.DeviceID = uint64(t.DeviceID)
	}
	if t.SubsystemID != 0 {
		out.SubsystemID = uint64(t.SubsystemID)
	}
	return out
}

// vgpuInstances expands the declared entries into one instance per slot, in
// declaration order. Load-time validation rejects an unknown type and an
// over-full GPU; a config that bypassed it is trimmed rather than trusted.
func (d *ConfigurableDevice) vgpuInstances(v *VGPUConfig) []VgpuInstance {
	types := vgpuTypesByName(v)
	ecc := d.vgpuECCMode()
	var out []VgpuInstance
	for _, entry := range v.Instances {
		t, ok := types[entry.Type]
		if !ok {
			warnLog("GPU %d: vGPU instance names undeclared type %q; skipped\n", d.index, entry.Type)
			continue
		}
		resolved := d.resolveVgpuType(t)
		for range max(entry.Count, 1) {
			slot := len(out)
			if slot >= maxVgpuInstancesPerGPU {
				warnLog("GPU %d: more than %d vGPU instances declared; the rest are skipped\n", d.index, maxVgpuInstancesPerGPU)
				return out
			}
			out = append(out, d.newVgpuInstance(resolved, slot, ecc))
		}
	}
	return out
}

// vgpuInstance builds only the instance in slot, numbering the slots as
// vgpuInstances does, so a lookup by ID does not derive every instance's
// identity on the GPU.
func (d *ConfigurableDevice) vgpuInstance(v *VGPUConfig, slot int) (VgpuInstance, bool) {
	if slot < 0 || slot >= maxVgpuInstancesPerGPU {
		return VgpuInstance{}, false
	}
	types := vgpuTypesByName(v)
	first := 0
	for _, entry := range v.Instances {
		t, ok := types[entry.Type]
		if !ok {
			continue
		}
		// Compared as an offset so an unvalidated count cannot overflow.
		n := max(entry.Count, 1)
		if slot-first < n {
			return d.newVgpuInstance(d.resolveVgpuType(t), slot, d.vgpuECCMode()), true
		}
		first += n
	}
	return VgpuInstance{}, false
}

func vgpuTypesByName(v *VGPUConfig) map[string]VGPUTypeConfig {
	types := make(map[string]VGPUTypeConfig, len(v.Types))
	for _, t := range v.Types {
		types[t.Name] = t
	}
	return types
}

func (d *ConfigurableDevice) vgpuECCMode() nvml.EnableState {
	if c := d.cfg().ECC; c != nil && c.ModeCurrent == "enabled" {
		return nvml.FEATURE_ENABLED
	}
	return nvml.FEATURE_DISABLED
}

func (d *ConfigurableDevice) newVgpuInstance(t VgpuType, slot int, ecc nvml.EnableState) VgpuInstance {
	return VgpuInstance{
		ID:            vgpuInstanceID(d.index, slot),
		Type:          t,
		UUID:          vgpuDerivedUUID(d.UUID, "vgpu", slot),
		VMID:          vgpuDerivedUUID(d.UUID, "vm", slot),
		ECCMode:       ecc,
		GPUInstanceID: vgpuNotMIGBacked,
	}
}

func vgpuInstanceID(physicalIndex, slot int) uint32 {
	return uint32((physicalIndex+1)*vgpuInstanceIDStride + slot + 1)
}

// vgpuInstanceSlot is the inverse of vgpuInstanceID for an ID already known to
// belong to the GPU. A block's base ID, (i+1)*stride, decodes to -1, which no
// instance has.
func vgpuInstanceSlot(id uint32) int {
	return int(id%vgpuInstanceIDStride) - 1
}

// vgpuDerivedUUID derives a stable UUID-shaped identity from the GPU's UUID.
// The salt keeps an instance's own UUID and its VM's apart. SHA-256 rather
// than a cheaper hash because nvidia-smi truncates VM IDs, so neighbouring
// slots must already differ in their leading digits.
func vgpuDerivedUUID(gpuUUID, salt string, slot int) string {
	b := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d", salt, gpuUUID, slot))
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
