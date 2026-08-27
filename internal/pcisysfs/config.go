// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package pcisysfs defines the schema for the PCI sysfs tree and renders it
// as a fake /sys/bus/pci/devices + /sys/devices/pciDDDD:BB tree under a
// configurable root directory.
package pcisysfs

// PCI is the per-device identity block. bus_id is the topology join key;
// device_id / subsystem_id are the NVML packed identity words the renderer
// unpacks into lspci-visible attribute files.
type PCI struct {
	BusID       string `json:"bus_id" yaml:"bus_id"`
	DeviceID    uint32 `json:"device_id,omitempty"    yaml:"device_id,omitempty"`
	SubsystemID uint32 `json:"subsystem_id,omitempty" yaml:"subsystem_id,omitempty"`
}

// PCIeTopology describes the root-complex layout the renderer materializes
// into sysfs. Each entry under root_complexes lists the devices attached to
// that root.
type PCIeTopology struct {
	RootComplexes []RootComplex `json:"root_complexes" yaml:"root_complexes"`
}

// RootComplex represents a single PCI host bridge (pciDDDD:BB in Linux sysfs)
// along with its NUMA node and the BDFs of every device underneath.
type RootComplex struct {
	ID       string   `json:"id"        yaml:"id"`
	NUMANode int      `json:"numa_node" yaml:"numa_node"`
	Devices  []string `json:"devices"   yaml:"devices"`
}
