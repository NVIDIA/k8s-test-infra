// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// Select returns the slots as a rack group runs them: each override, in order,
// replaces the fields it sets in the slots it names. An override that replaces
// the profile also drops the slot's configuration, firmware version, VPD and a
// systemImageGUID that names an RDMA device, since those belong to the old
// model; the new profile's own apply unless the override sets them. A
// distinct systemImageGUID names no device, so it carries over. The slots
// passed in are left unchanged.
func Select(slots []v1alpha1.NICSlot, overrides []v1alpha1.NICOverride) ([]v1alpha1.NICSlot, error) {
	out := make([]v1alpha1.NICSlot, len(slots))
	index := make(map[string]int, len(slots))
	for i := range slots {
		slots[i].DeepCopyInto(&out[i])
		index[slots[i].Slot] = i
	}

	var errs []error
	for n, o := range overrides {
		if len(o.Slots) == 0 {
			errs = append(errs, fmt.Errorf("NIC override %d names no slots", n))
		}
		for _, name := range o.Slots {
			i, ok := index[name]
			if !ok {
				errs = append(errs, fmt.Errorf("NIC override %d: unknown slot %q", n, name))
				continue
			}
			applyOverride(&out[i], &o)
		}
	}
	return out, errors.Join(errs...)
}

func applyOverride(s *v1alpha1.NICSlot, o *v1alpha1.NICOverride) {
	if o.Profile != "" && o.Profile != s.Profile {
		s.Profile, s.Configuration, s.FirmwareVersion, s.VPD = o.Profile, "", "", nil
		if s.SystemImageGUID != v1alpha1.SystemImageGUIDDistinct {
			s.SystemImageGUID = ""
		}
	}
	if o.SystemImageGUID != "" {
		s.SystemImageGUID = o.SystemImageGUID
	}
	if o.Configuration != "" {
		s.Configuration = o.Configuration
	}
	if o.FirmwareVersion != "" {
		s.FirmwareVersion = o.FirmwareVersion
	}
	if len(o.VPD) > 0 {
		if s.VPD == nil {
			s.VPD = map[string]string{}
		}
		maps.Copy(s.VPD, o.VPD)
	}
}

// ValidateNode checks a Node's NIC slots, as the overrides leave them, against
// its GPU slots and the profiles the slots name. The profiles themselves are
// checked by ValidateProfile.
func ValidateNode(topology *v1alpha1.SGPUTopology, overrides []v1alpha1.NICOverride, profiles Profiles) error {
	slots, err := Select(topology.NICs, overrides)
	var pr problems
	pr.add(err)
	place := newNodePlacement(&pr)
	place.addRootPorts(topology.RootPorts)
	gpus := validateGPUSlots(&pr, topology.GPUSlots, place)

	names := map[string]bool{}
	boards := map[int32]string{} // board -> profile
	overridden := overriddenSlots(overrides)
	for i := range slots {
		s := &slots[i]
		where := "slot " + s.Slot
		if overridden[s.Slot] {
			where += " (with NIC overrides)"
		}
		pr.check(s.Slot != "" && !names[s.Slot], "slot name %q is empty or used twice", s.Slot)
		names[s.Slot] = true
		validateBoard(&pr, where, s, boards)
		for _, g := range s.GPUs {
			pr.check(gpus[g], "%s: GPU %d has no GPU slot", where, g)
		}
		p, c := selected(&pr, where, s, profiles)
		validateSlotPlacement(&pr, where, s, p, place)
		if c != nil {
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

// selected returns the slot's profile and configuration, or a nil
// configuration when either is unknown.
func selected(pr *problems, where string, s *v1alpha1.NICSlot, profiles Profiles) (*v1alpha1.SNICProfile, *v1alpha1.NICConfiguration) {
	p, ok := profiles[s.Profile]
	if !ok {
		pr.check(false, "%s: unknown profile %q", where, s.Profile)
		return nil, nil
	}
	c, ok := p.Spec.Configuration(s.Configuration)
	pr.check(ok, "%s: profile %s has no configuration %q", where, s.Profile, s.Configuration)
	return p, c
}

func validateSlot(pr *problems, where string, s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, c *v1alpha1.NICConfiguration,
	place *nodePlacement) {
	validateSlotVPD(pr, where, s, p.Spec.VPD)
	g := s.SystemImageGUID
	pr.check(g == "" || g == v1alpha1.SystemImageGUIDDistinct || hasRDMADevice(c, g),
		"%s: systemImageGUID %q is neither %q nor an RDMA device of configuration %s",
		where, g, v1alpha1.SystemImageGUIDDistinct, c.Name)

	for _, f := range c.Functions {
		d, ok := s.Devices[f.PCIDevice]
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
		for _, name := range slices.Sorted(maps.Keys(s.Devices)) {
			pr.check(s.Devices[name].VPDPosition != "",
				"%s: device %s: VPD %s is per board position, but the device has no vpdPosition", where, name, k)
		}
	}
}

// validateSlotPlacement checks where the slot puts its PCI devices and adds
// its switch ports to the Node's tree. Placement doesn't depend on the
// profile, so a slot whose profile or configuration is unknown (p nil) still
// places its switch ports, and what sits below them reports nothing more.
func validateSlotPlacement(pr *problems, where string, s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, place *nodePlacement) {
	for _, name := range slices.Sorted(maps.Keys(s.Devices)) {
		validatePlacement(pr, fmt.Sprintf("%s: device %s", where, name), s.Devices[name], p, where, place)
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

func overriddenSlots(overrides []v1alpha1.NICOverride) map[string]bool {
	out := map[string]bool{}
	for _, o := range overrides {
		for _, s := range o.Slots {
			out[s] = true
		}
	}
	return out
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
