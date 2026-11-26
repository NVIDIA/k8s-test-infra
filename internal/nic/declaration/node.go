// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// Select returns the configuration each slot runs, by slot name: the one a
// configuration choice names, else its profile's default. A slot whose profile
// is unknown, or whose configuration its profile lacks, maps to nil, and the
// error says why. Each choice names existing slots, and a slot appears in one
// choice at most.
func Select(slots []v1alpha1.NICSlot, choices []v1alpha1.NICConfigurationChoice,
	profiles Profiles) (map[string]*v1alpha1.NICConfiguration, error) {
	var pr problems
	chosen, choice := chooseSlots(&pr, slots, choices)
	out := make(map[string]*v1alpha1.NICConfiguration, len(slots))
	for i := range slots {
		s := &slots[i]
		p, ok := profiles[s.Profile]
		if !ok {
			out[s.Slot] = nil // ValidateNode reports the unknown profile
			continue
		}
		name, isChosen := chosen[s.Slot]
		c, ok := p.Spec.Configuration(name)
		switch {
		case !ok && isChosen:
			pr.check(false, "NIC configuration choice %d: profile %s of slot %s has no configuration %q",
				choice[s.Slot], s.Profile, s.Slot, name)
		case !ok:
			pr.check(false, "slot %s: profile %s has no configuration %q, its default", s.Slot, s.Profile,
				p.Spec.DefaultConfiguration)
		}
		out[s.Slot] = c
	}
	return out, pr.err()
}

// chooseSlots checks the configuration choices and returns the configuration
// each slot they name runs and the choice that names it.
func chooseSlots(pr *problems, slots []v1alpha1.NICSlot,
	choices []v1alpha1.NICConfigurationChoice) (chosen map[string]string, choice map[string]int) {
	known := make(map[string]bool, len(slots))
	for i := range slots {
		known[slots[i].Slot] = true
	}
	chosen, choice = map[string]string{}, map[string]int{}
	for n, c := range choices {
		where := fmt.Sprintf("NIC configuration choice %d", n)
		pr.check(len(c.Slots) > 0, "%s names no slots", where)
		pr.check(c.Configuration != "", "%s names no configuration", where)
		for _, name := range c.Slots {
			if !known[name] {
				pr.check(false, "%s: unknown slot %q", where, name)
				continue
			}
			if prev, twice := choice[name]; twice {
				pr.check(false, "%s: slot %s is already in choice %d", where, name, prev)
				continue
			}
			choice[name], chosen[name] = n, c.Configuration
		}
	}
	return chosen, choice
}

// ValidateNode checks a Node's NIC slots, each running the configuration the
// choices select, against its GPU slots and the profiles the slots name. The
// profiles themselves are checked by ValidateProfile.
func ValidateNode(topology *v1alpha1.SGPUTopology, choices []v1alpha1.NICConfigurationChoice, profiles Profiles) error {
	configs, err := Select(topology.NICs, choices, profiles)
	var pr problems
	pr.add(err)
	place := newNodePlacement(&pr)
	place.addRootPorts(topology.RootPorts)
	gpus := validateGPUSlots(&pr, topology.GPUSlots, place)

	names := map[string]bool{}
	boards := map[int32]string{} // board -> profile
	for i := range topology.NICs {
		s := &topology.NICs[i]
		where := "slot " + s.Slot
		pr.check(s.Slot != "" && !names[s.Slot], "slot name %q is empty or used twice", s.Slot)
		names[s.Slot] = true
		validateBoard(&pr, where, s, boards)
		for _, g := range s.GPUs {
			pr.check(gpus[g], "%s: GPU %d has no GPU slot", where, g)
		}
		p, ok := profiles[s.Profile]
		pr.check(ok, "%s: unknown profile %q", where, s.Profile)
		if !ok {
			p = nil
		}
		validateSlotPlacement(&pr, where, s, p, place)
		if c := configs[s.Slot]; c != nil {
			validateSlot(&pr, where, s, p, c, place)
		}
	}
	place.checkTree()
	return pr.err()
}

// ValidateRDMA checks a Node's RDMA software settings.
func ValidateRDMA(rdma *v1alpha1.RDMASoftware) error {
	if rdma != nil && !optional(rdma.UMADMode, umadMode) {
		return fmt.Errorf("software.rdma.umadMode %q is not an octal file mode", rdma.UMADMode)
	}
	return nil
}

// validateGPUSlots checks the GPU slots and returns their indexes.
func validateGPUSlots(pr *problems, slots []v1alpha1.GPUSlot, place *nodePlacement) map[int32]bool {
	indexes := map[int32]bool{}
	for _, g := range slots {
		pr.check(!indexes[g.Index], "GPU slot %d is declared twice", g.Index)
		indexes[g.Index] = true
		pr.check(rootComplex.MatchString(g.RootComplex), "GPU slot %d: rootComplex %q is not pciDDDD:BB", g.Index, g.RootComplex)
		pr.check(inDomain(bdf, g.PCIAddress, domainOf(g.RootComplex)),
			"GPU slot %d: pciAddress %q is not a PCI address in %s", g.Index, g.PCIAddress, g.RootComplex)
		place.claim(g.PCIAddress, fmt.Sprintf("GPU %d", g.Index))
		if g.Parent != "" {
			place.addParent(fmt.Sprintf("GPU slot %d", g.Index), g.Parent, g.RootComplex, g.PCIAddress)
		}
	}
	return indexes
}

// validateBoard checks that board numbers, which are Node-wide, each hold one
// NIC model.
func validateBoard(pr *problems, where string, s *v1alpha1.NICSlot, boards map[int32]string) {
	if s.Board == nil {
		return
	}
	pr.check(*s.Board >= 0, "%s: board %d is negative", where, *s.Board)
	prev, ok := boards[*s.Board]
	pr.check(!ok || prev == s.Profile, "%s: board %d holds both %s and %s", where, *s.Board, prev, s.Profile)
	boards[*s.Board] = s.Profile
}

func validateSlot(pr *problems, where string, s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, c *v1alpha1.NICConfiguration,
	place *nodePlacement) {
	validateSlotVPD(pr, where, s, p.Spec.VPD)
	g := s.SystemImageGUID
	pr.check(g == "" || g == v1alpha1.SystemImageGUIDDistinct || hasRDMADevice(c, g),
		"%s: systemImageGUID %q is neither %q nor an RDMA device of configuration %s",
		where, g, v1alpha1.SystemImageGUIDDistinct, c.Name)

	for _, f := range c.Functions {
		d, ok := s.Device(f.PCIDevice)
		pr.check(ok, "%s: configuration %s puts function %s on PCI device %q, which the slot doesn't place",
			where, c.Name, f.ID, f.PCIDevice)
		if ok {
			place.claim(fmt.Sprintf("%s.%d", d.Address, f.Function), where)
		}
	}
}

// EffectiveScope is the scope that shares one value of identity keyword k:
// its own, or that of the identity keyword it reuses.
func EffectiveScope(v *v1alpha1.NICVPD, k string) v1alpha1.VPDScope {
	scope := v.Identities[k]
	if ref, ok := v.Identities[string(scope)]; ok {
		return ref
	}
	return scope
}

// validateSlotVPD checks that the slot gives each shared VPD identity what its
// scope needs: a board, or a VPD position on every device.
func validateSlotVPD(pr *problems, where string, s *v1alpha1.NICSlot, v *v1alpha1.NICVPD) {
	if v == nil {
		return
	}
	for _, k := range slices.Sorted(maps.Keys(v.Identities)) {
		scope := EffectiveScope(v, k)
		// A board position is a position on a board, so both scopes need one;
		// without it, two boardless slots would share an identity.
		pr.check(scope == v1alpha1.VPDScopeASIC || s.Board != nil,
			"%s: VPD %s is per %s, but the slot names no board", where, k, scope)
		if scope != v1alpha1.VPDScopeBoardPosition {
			continue
		}
		for _, d := range s.Devices {
			pr.check(d.VPDPosition != "",
				"%s: device %s: VPD %s is per board position, but the device has no vpdPosition", where, d.Name, k)
		}
	}
}

// validateSlotPlacement checks where the slot puts its PCI devices and adds
// its switch ports to the Node's tree. Placement doesn't depend on the
// profile, so a slot whose profile or configuration is unknown (p nil) still
// places its switch ports, and what sits below them reports nothing more.
func validateSlotPlacement(pr *problems, where string, s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, place *nodePlacement) {
	pr.check(len(s.Devices) > 0, "%s: places no devices", where)
	placed := map[string]bool{}
	for _, d := range s.Devices {
		pr.check(d.Name != "" && !placed[d.Name], "%s: device name %q is empty or used twice", where, d.Name)
		placed[d.Name] = true
		validatePlacement(pr, fmt.Sprintf("%s: device %s", where, d.Name), d, p, where, place)
	}
}

func validatePlacement(pr *problems, where string, d v1alpha1.NICDevicePlacement, p *v1alpha1.SNICProfile, owner string,
	place *nodePlacement) {
	domain := domainOf(d.RootComplex)
	pr.check(rootComplex.MatchString(d.RootComplex), "%s: rootComplex %q is not pciDDDD:BB", where, d.RootComplex)
	pr.check(d.Parent != "" || len(d.Bridges) == 0, "%s: a device on the root bus has no switch ports", where)
	pr.check(d.Parent != "" || onRootBus(d.Address, d.RootComplex), "%s: has no parent, but %s isn't on %s's root bus",
		where, d.Address, d.RootComplex)
	if p != nil {
		pr.check(len(d.Bridges) == 0 || p.Spec.SwitchPort != nil,
			"%s: bridges are declared, but profile %s has no switchPort", where, p.Name)
	}
	for _, b := range d.Bridges {
		pr.check(inDomain(bdf, b.Address, domain), "%s: bridge %q is not a PCI address in %s", where, b.Address, d.RootComplex)
		place.claim(b.Address, owner)
		place.addSwitchPort(b, d.RootComplex, where)
	}
	pr.check(inDomain(deviceAddr, d.Address, domain), "%s: address %q is not DDDD:BB:DD in %s", where, d.Address, d.RootComplex)
	if d.Parent != "" {
		place.addParent(where, d.Parent, d.RootComplex, d.Address)
	}
	pr.check(d.NUMANode >= -1, "%s: numaNode %d is below -1", where, d.NUMANode)
}

func hasRDMADevice(c *v1alpha1.NICConfiguration, id string) bool {
	return slices.ContainsFunc(c.RDMADevices, func(d v1alpha1.NICRDMADevice) bool { return d.ID == id })
}

// inDomain reports whether addr has the form re describes and sits in domain.
func inDomain(re *regexp.Regexp, addr, domain string) bool {
	return re.MatchString(addr) && strings.HasPrefix(addr, domain)
}

// domainOf returns "dddd:" for a root complex "pcidddd:bb", or "" for a
// malformed one, which the caller reports.
func domainOf(rc string) string {
	if !rootComplex.MatchString(rc) {
		return ""
	}
	return rc[3:7] + ":"
}
