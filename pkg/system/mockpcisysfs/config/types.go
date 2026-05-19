// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package config defines the YAML schema for the `pcie_topology:` block
// embedded in mock-nvml profile configs. The renderer consumes this to
// populate a fake `/sys/bus/pci/devices` + `/sys/devices/pciDDDD:BB` tree
// under MOCK_PCI_ROOT.
//
// Keeping the schema in a standalone package (no cgo dependency on the
// mocknvml engine) mirrors `pkg/network/mockibsysfs/config` and lets the
// renderer CLI build into a small static binary.
package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Profile is the minimal slice of the mock-nvml profile YAML that the PCI
// sysfs renderer cares about. It deliberately ignores every GPU detail
// other than the per-device bus_id, which is the join key into the
// topology block.
type Profile struct {
	Devices      []Device      `json:"devices"      yaml:"devices"`
	PCIeTopology *PCIeTopology `json:"pcie_topology,omitempty" yaml:"pcie_topology,omitempty"`
}

// Device captures only the fields the renderer needs: the device index
// (used for --gpu-count clipping) and the PCI bus_id. Every other profile
// field is ignored at unmarshal time.
type Device struct {
	Index int `json:"index" yaml:"index"`
	PCI   PCI `json:"pci"   yaml:"pci"`
}

// PCI is the inner block on each device entry. Only bus_id matters here.
type PCI struct {
	BusID string `json:"bus_id" yaml:"bus_id"`
}

// PCIeTopology describes the root-complex layout that the renderer
// materializes into sysfs. Each entry under root_complexes lists the
// devices physically attached to that root.
type PCIeTopology struct {
	RootComplexes []RootComplex `json:"root_complexes" yaml:"root_complexes"`
}

// RootComplex represents a single PCI host bridge (`pciDDDD:BB` in real
// Linux sysfs) along with the NUMA node it lives on and the BDFs of
// every device attached underneath.
type RootComplex struct {
	// ID is the sysfs directory name, e.g. "pci0000:00". Must start with
	// "pci" and match the kernel's printf format ("pciDDDD:BB").
	ID string `json:"id" yaml:"id"`

	// NUMANode is the value written into each child device's `numa_node`
	// file. Real x86 hosts use -1 for "no affinity"; the renderer accepts
	// any int (including negatives) verbatim.
	NUMANode int `json:"numa_node" yaml:"numa_node"`

	// Devices is the list of GPU PCI BDFs (4-digit-domain form, e.g.
	// "0000:07:00.0") that live under this root complex.
	Devices []string `json:"devices" yaml:"devices"`
}

// bdfRE matches a canonical Linux sysfs BDF ("0000:07:00.0"). NVML's
// `busIdLegacy` 8-digit form is intentionally rejected here so the
// renderer fails loudly if a profile still carries it.
var bdfRE = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$`)

// rootRE matches a sysfs root-complex directory name ("pci0000:00").
var rootRE = regexp.MustCompile(`^pci[0-9a-fA-F]{4}:[0-9a-fA-F]{2}$`)

// Validate cross-checks the topology against the device list. It returns
// the first violation encountered:
//   - root complex IDs are well-formed and unique
//   - every device BDF is well-formed (4-digit domain) and unique across
//     the whole topology
//   - every BDF mentioned in the topology also exists in `devices:`
//
// The reverse implication is intentionally NOT enforced: profiles may
// declare devices that aren't part of the rendered topology yet, and
// the renderer treats those as "no sysfs entry" rather than an error.
func (p *Profile) Validate() error {
	if p.PCIeTopology == nil {
		return nil
	}

	devSet := make(map[string]struct{}, len(p.Devices))
	for _, d := range p.Devices {
		if d.PCI.BusID == "" {
			continue
		}
		devSet[strings.ToLower(d.PCI.BusID)] = struct{}{}
	}

	seenRoot := make(map[string]struct{}, len(p.PCIeTopology.RootComplexes))
	seenBDF := make(map[string]struct{})
	for _, rc := range p.PCIeTopology.RootComplexes {
		if !rootRE.MatchString(rc.ID) {
			return fmt.Errorf("pcie_topology: invalid root complex id %q (want %q)", rc.ID, "pciDDDD:BB")
		}
		key := strings.ToLower(rc.ID)
		if _, dup := seenRoot[key]; dup {
			return fmt.Errorf("pcie_topology: duplicate root complex %q", rc.ID)
		}
		seenRoot[key] = struct{}{}

		for _, bdf := range rc.Devices {
			if !bdfRE.MatchString(bdf) {
				return fmt.Errorf("pcie_topology: invalid device BDF %q under %q (want DDDD:BB:DD.F; the 8-digit busIdLegacy form is not accepted)", bdf, rc.ID)
			}
			lb := strings.ToLower(bdf)
			if _, dup := seenBDF[lb]; dup {
				return fmt.Errorf("pcie_topology: device %q appears under multiple root complexes", bdf)
			}
			seenBDF[lb] = struct{}{}

			if _, ok := devSet[lb]; !ok {
				return fmt.Errorf("pcie_topology: device %q (under %q) is not declared in `devices:`", bdf, rc.ID)
			}
		}
	}
	return nil
}

// DefaultTopology synthesizes a single-root topology covering every
// device in the profile. Use this when the profile omits an explicit
// `pcie_topology:` block — the resulting tree is well-formed but flat
// (every GPU shares one root complex and NUMA node).
func (p *Profile) DefaultTopology() *PCIeTopology {
	rc := RootComplex{ID: "pci0000:00", NUMANode: 0}
	for _, d := range p.Devices {
		if d.PCI.BusID == "" {
			continue
		}
		rc.Devices = append(rc.Devices, strings.ToLower(d.PCI.BusID))
	}
	if len(rc.Devices) == 0 {
		return nil
	}
	return &PCIeTopology{RootComplexes: []RootComplex{rc}}
}

// EffectiveTopology returns the explicit topology if set, otherwise the
// flat default. Returns nil only when the profile has no devices at all.
func (p *Profile) EffectiveTopology() *PCIeTopology {
	if p.PCIeTopology != nil && len(p.PCIeTopology.RootComplexes) > 0 {
		return p.PCIeTopology
	}
	return p.DefaultTopology()
}
