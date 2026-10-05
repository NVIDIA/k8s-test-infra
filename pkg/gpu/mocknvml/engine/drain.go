// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// The drain calls behind `nvidia-smi drain`: the driver's view of which GPUs
// it manages, as distinct from which GPUs a process enumerates.
//
// They address a GPU by PCI location rather than by handle because the GPU
// they act on may not be enumerable: a draining GPU drops out of enumeration
// for every process that initialises NVML afterwards, and that is exactly the
// GPU a later call has to undrain. The state is driver state, so it is kept in
// the override document where every process on the node reads it.

import (
	"fmt"
	"slices"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// QueryDrainState reports whether the GPU at pci is draining.
func (e *Engine) QueryDrainState(pci *nvml.PciInfo) (nvml.EnableState, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 {
		return nvml.FEATURE_DISABLED, nvml.ERROR_UNINITIALIZED
	}
	d := e.server.deviceAtPCI(pci)
	if d == nil {
		debugLog("[NVML] nvmlDeviceQueryDrainState -> INVALID_ARGUMENT (no GPU at %s)\n", describePCI(pci))
		return nvml.FEATURE_DISABLED, nvml.ERROR_INVALID_ARGUMENT
	}
	state := nvml.FEATURE_DISABLED
	if d.cfg().Draining {
		state = nvml.FEATURE_ENABLED
	}
	debugLog("[NVML] nvmlDeviceQueryDrainState(%s) -> %d\n", d.PciBusID, state)
	return state, nvml.SUCCESS
}

// ModifyDrainState enters or leaves the draining state.
func (e *Engine) ModifyDrainState(pci *nvml.PciInfo, state nvml.EnableState) nvml.Return {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 {
		return nvml.ERROR_UNINITIALIZED
	}
	if state != nvml.FEATURE_ENABLED && state != nvml.FEATURE_DISABLED {
		debugLog("[NVML] nvmlDeviceModifyDrainState(%d) -> INVALID_ARGUMENT\n", state)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d := e.server.deviceAtPCI(pci)
	if d == nil {
		debugLog("[NVML] nvmlDeviceModifyDrainState -> INVALID_ARGUMENT (no GPU at %s)\n", describePCI(pci))
		return nvml.ERROR_INVALID_ARGUMENT
	}
	return d.setDraining(state == nvml.FEATURE_ENABLED)
}

func (d *ConfigurableDevice) setDraining(draining bool) nvml.Return {
	// Persistence mode holds the GPU open, so NVML refuses to drain it until
	// the operator turns persistence off.
	if draining && d.persistenceEnabled() {
		debugLog("[NVML] nvmlDeviceModifyDrainState(%s) -> IN_USE (persistence mode enabled)\n", d.PciBusID)
		return nvml.ERROR_IN_USE
	}
	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] nvmlDeviceModifyDrainState(%s) -> NO_PERMISSION (no override writer)\n", d.PciBusID)
		return nvml.ERROR_NO_PERMISSION
	}
	if err := w.SetDrainState(d.PhysicalIndex(), draining); err != nil {
		warnLog("[NVML] nvmlDeviceModifyDrainState(%s) -> NO_PERMISSION: %v\n", d.PciBusID, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] nvmlDeviceModifyDrainState(%s, draining=%t)\n", d.PciBusID, draining)
	return nvml.SUCCESS
}

// RemoveGpu detaches the GPU at pci from the driver. Every process that
// enumerates GPUs afterwards — this one included — sees one fewer, with the
// GPUs after it renumbered. gpuState and linkState describe what happens to
// the PCI device and its upstream link, which the mock does not model beyond
// validating them.
func (e *Engine) RemoveGpu(pci *nvml.PciInfo, gpuState nvml.DetachGpuState, linkState nvml.PcieLinkState) nvml.Return {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initCount == 0 {
		return nvml.ERROR_UNINITIALIZED
	}
	if gpuState != nvml.DETACH_GPU_KEEP && gpuState != nvml.DETACH_GPU_REMOVE {
		debugLog("[NVML] nvmlDeviceRemoveGpu -> INVALID_ARGUMENT (gpuState %d)\n", gpuState)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if linkState != nvml.PCIE_LINK_KEEP && linkState != nvml.PCIE_LINK_SHUT_DOWN {
		debugLog("[NVML] nvmlDeviceRemoveGpu -> INVALID_ARGUMENT (linkState %d)\n", linkState)
		return nvml.ERROR_INVALID_ARGUMENT
	}
	d := e.server.deviceAtPCI(pci)
	if d == nil {
		debugLog("[NVML] nvmlDeviceRemoveGpu -> INVALID_ARGUMENT (no GPU at %s)\n", describePCI(pci))
		return nvml.ERROR_INVALID_ARGUMENT
	}
	if ret := d.remove(); ret != nvml.SUCCESS {
		return ret
	}
	e.refreshEnumeration(true)
	return nvml.SUCCESS
}

func (d *ConfigurableDevice) remove() nvml.Return {
	// NVML refuses while anything is attached. Persistence mode counts as an
	// attachment, and a configured process is one that is running on the GPU.
	c := d.cfg()
	if c.PersistenceMode == "enabled" || len(c.Processes) > 0 {
		debugLog("[NVML] nvmlDeviceRemoveGpu(%s) -> IN_USE (persistence=%q, %d processes)\n",
			d.PciBusID, c.PersistenceMode, len(c.Processes))
		return nvml.ERROR_IN_USE
	}
	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] nvmlDeviceRemoveGpu(%s) -> NO_PERMISSION (no override writer)\n", d.PciBusID)
		return nvml.ERROR_NO_PERMISSION
	}
	if err := w.SetRemoved(d.PhysicalIndex(), true); err != nil {
		warnLog("[NVML] nvmlDeviceRemoveGpu(%s) -> NO_PERMISSION: %v\n", d.PciBusID, err)
		return nvml.ERROR_NO_PERMISSION
	}
	configOverrides.invalidateAfterLocalWrite()
	debugLog("[NVML] nvmlDeviceRemoveGpu(%s) removed\n", d.PciBusID)
	return nvml.SUCCESS
}

// DiscoverGpus brings back removed GPUs under pci: every removed GPU when its
// domain, bus and device are all zero, otherwise the one at that address.
func (e *Engine) DiscoverGpus(pci *nvml.PciInfo) nvml.Return {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initCount == 0 {
		return nvml.ERROR_UNINITIALIZED
	}
	if pci == nil {
		return nvml.ERROR_INVALID_ARGUMENT
	}
	w := overrideWriter()
	if w == nil {
		warnLog("[NVML] nvmlDeviceDiscoverGpus -> NO_PERMISSION (no override writer)\n")
		return nvml.ERROR_NO_PERMISSION
	}
	ret := nvml.SUCCESS
	for _, d := range e.server.removedUnder(pci) {
		if err := d.rediscover(w); err != nil {
			warnLog("[NVML] nvmlDeviceDiscoverGpus(%s) -> UNKNOWN: %v\n", d.PciBusID, err)
			ret = nvml.ERROR_UNKNOWN
			break
		}
		debugLog("[NVML] nvmlDeviceDiscoverGpus rediscovered %s\n", d.PciBusID)
	}
	// GPUs rediscovered before a failure are back on the node either way.
	configOverrides.invalidateAfterLocalWrite()
	e.refreshEnumeration(true)
	return ret
}

// removedUnder lists the removed GPUs DiscoverGpus scans for pci.
func (s *MockServer) removedUnder(pci *nvml.PciInfo) []*ConfigurableDevice {
	wholeTree := pci.Domain == 0 && pci.Bus == 0 && pci.Device == 0
	var out []*ConfigurableDevice
	for _, d := range s.configurableDevices {
		if d != nil && d.cfg().Removed && (wholeTree || d.atLocation(pci.Domain, pci.Bus, pci.Device)) {
			out = append(out, d)
		}
	}
	return out
}

// rediscover re-attaches a removed GPU. The driver probes it afresh, so it
// does not come back draining. Undrain first: if the second write fails the
// GPU is still removed, and a retry finds it again.
func (d *ConfigurableDevice) rediscover(w OverrideWriter) error {
	if err := w.SetDrainState(d.PhysicalIndex(), false); err != nil {
		return fmt.Errorf("clearing drain state: %w", err)
	}
	if err := w.SetRemoved(d.PhysicalIndex(), false); err != nil {
		return fmt.Errorf("clearing removal: %w", err)
	}
	return nil
}

// ExcludedDevices lists the GPUs the kernel module was told to skip, in
// physical order.
func (e *Engine) ExcludedDevices() ([]nvml.ExcludedDeviceInfo, nvml.Return) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.initCount == 0 {
		return nil, nvml.ERROR_UNINITIALIZED
	}
	excluded := []nvml.ExcludedDeviceInfo{}
	for _, d := range e.server.configurableDevices {
		if d == nil || !d.cfg().Excluded {
			continue
		}
		info := nvml.ExcludedDeviceInfo{PciInfo: d.pciInfo}
		writeBusID(info.Uuid[:], d.UUID)
		excluded = append(excluded, info)
	}
	return excluded, nvml.SUCCESS
}

// enumerable narrows present — the GPUs whose device nodes this process can
// open, nil for all of them — to the ones NVML enumerates: those the driver
// manages and that are not draining. A drain only keeps a GPU out of processes
// that look for it afterwards, so keepVisible keeps a draining GPU this
// process already enumerates. It keeps nil when nothing is hidden, so an
// unfiltered node stays unfiltered.
func (s *MockServer) enumerable(present []int, keepVisible bool) []int {
	out := []int{}
	hidden := false
	for index, d := range s.configurableDevices {
		if d == nil || (present != nil && !slices.Contains(present, index)) {
			continue
		}
		draining := d.cfg().Draining && !(keepVisible && s.isDeviceVisible(index))
		if !d.managed() || draining {
			hidden = true
			continue
		}
		out = append(out, index)
	}
	if present == nil && !hidden {
		return nil
	}
	return out
}

// managed reports whether the driver manages the GPU: it was neither removed
// nor excluded from the kernel module.
func (d *ConfigurableDevice) managed() bool {
	c := d.cfg()
	return !c.Removed && !c.Excluded
}

func (d *ConfigurableDevice) atLocation(domain, bus, device uint32) bool {
	return d.pciInfo.Domain == domain && d.pciInfo.Bus == bus && d.pciInfo.Device == device
}

// deviceAtPCI returns the managed GPU at pci whether or not this process
// enumerates it, or nil when there is none. nvmlPciInfo_t has no function
// field, so a GPU is identified by domain, bus and device.
func (s *MockServer) deviceAtPCI(pci *nvml.PciInfo) *ConfigurableDevice {
	domain, bus, device, ok := pciLocation(pci)
	if !ok {
		return nil
	}
	for _, d := range s.configurableDevices {
		if d != nil && d.managed() && d.atLocation(domain, bus, device) {
			return d
		}
	}
	return nil
}

// pciLocation reads the address a caller passed. The numeric fields are what
// NVML documents, but a caller may fill only the busId string; an all-zero
// address cannot be a GPU, so it falls back to parsing the string.
func pciLocation(pci *nvml.PciInfo) (domain, bus, device uint32, ok bool) {
	if pci == nil {
		return 0, 0, 0, false
	}
	if pci.Domain != 0 || pci.Bus != 0 || pci.Device != 0 {
		return pci.Domain, pci.Bus, pci.Device, true
	}
	domain, bus, device, _, err := ParsePCIBusID(busIDString(pci.BusId[:]))
	if err != nil {
		return 0, 0, 0, false
	}
	return domain, bus, device, true
}

// busIDString decodes an NVML char array up to its NUL terminator; it is the
// inverse of writeBusID and generic for the same reason.
func busIDString[E ~int8 | ~uint8](src []E) string {
	b := make([]byte, 0, len(src))
	for _, c := range src {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

func describePCI(pci *nvml.PciInfo) string {
	domain, bus, device, ok := pciLocation(pci)
	if !ok {
		return "<invalid>"
	}
	return formatPCIBusID(domain, bus, device, 0)
}
