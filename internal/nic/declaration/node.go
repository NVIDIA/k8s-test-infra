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
// the profile drops the slot's configuration, since configuration names belong
// to a profile; the new profile's default applies unless the override names
// one. The slots passed in are left unchanged.
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
		s.Profile, s.Configuration = o.Profile, ""
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
	book := addressBook{owners: map[string]string{}, pr: &pr}
	gpus := validateGPUSlots(&pr, topology.GPUSlots, book)

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
		if p, c := selected(&pr, where, s, profiles); c != nil {
			validateSlot(&pr, where, s, p, c, book)
		}
	}
	return pr.err()
}

// ValidateRDMA checks a Node's RDMA software settings.
func ValidateRDMA(rdma *v1alpha1.RDMASoftware) error {
	if rdma != nil && !optional(rdma.UMADMode, umadMode) {
		return fmt.Errorf("software.rdma.umadMode %q is not an octal file mode", rdma.UMADMode)
	}
	return nil
}

// addressBook makes sure no two things on a Node claim one PCI address.
type addressBook struct {
	owners map[string]string
	pr     *problems
}

func (a addressBook) claim(addr, owner string) {
	prev, taken := a.owners[addr]
	a.pr.check(!taken, "PCI address %s is used by %s and %s", addr, prev, owner)
	a.owners[addr] = owner
}

// validateGPUSlots checks the GPU slots and returns their indexes.
func validateGPUSlots(pr *problems, slots []v1alpha1.GPUSlot, book addressBook) map[int32]bool {
	indexes := map[int32]bool{}
	for _, g := range slots {
		pr.check(!indexes[g.Index], "GPU slot %d is declared twice", g.Index)
		indexes[g.Index] = true
		pr.check(bdf.MatchString(g.PCIAddress), "GPU slot %d: pciAddress %q is not a PCI address", g.Index, g.PCIAddress)
		pr.check(rootComplex.MatchString(g.RootComplex), "GPU slot %d: rootComplex %q is not pciDDDD:BB", g.Index, g.RootComplex)
		book.claim(g.PCIAddress, fmt.Sprintf("GPU %d", g.Index))
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
	book addressBook) {
	validateSlotVPD(pr, where, s, p.Spec.VPD)
	g := s.SystemImageGUID
	pr.check(g == "" || g == v1alpha1.SystemImageGUIDDistinct || hasRDMADevice(c, g),
		"%s: systemImageGUID %q is neither %q nor an RDMA device of configuration %s",
		where, g, v1alpha1.SystemImageGUIDDistinct, c.Name)

	for _, name := range slices.Sorted(maps.Keys(s.Devices)) {
		validatePlacement(pr, fmt.Sprintf("%s: device %s", where, name), s.Devices[name], p, where, book)
	}
	for _, f := range c.Functions {
		d, ok := s.Devices[f.PCIDevice]
		pr.check(ok, "%s: configuration %s puts function %s on PCI device %q, which the slot doesn't place",
			where, c.Name, f.ID, f.PCIDevice)
		if ok {
			book.claim(fmt.Sprintf("%s.%d", d.Address, f.Function), where)
		}
	}
}

// validateSlotVPD checks that the slot gives each shared VPD identity what its
// scope needs: a board, or a VPD position on every device.
func validateSlotVPD(pr *problems, where string, s *v1alpha1.NICSlot, v *v1alpha1.NICVPD) {
	if v == nil {
		return
	}
	for _, k := range slices.Sorted(maps.Keys(v.Identities)) {
		scope := v.Identities[k]
		pr.check(scope != v1alpha1.VPDScopeBoard || s.Board != nil,
			"%s: VPD %s is per board, but the slot names no board", where, k)
		if scope != v1alpha1.VPDScopeBoardPosition {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(s.Devices)) {
			pr.check(s.Devices[name].VPDPosition != "",
				"%s: device %s: VPD %s is per board position, but the device has no vpdPosition", where, name, k)
		}
	}
}

func validatePlacement(pr *problems, where string, d v1alpha1.NICDevicePlacement, p *v1alpha1.SNICProfile, owner string,
	book addressBook) {
	domain := domainOf(d.RootComplex)
	pr.check(rootComplex.MatchString(d.RootComplex), "%s: rootComplex %q is not pciDDDD:BB", where, d.RootComplex)
	pr.check(d.RootPort != "" || (d.Parent == "" && len(d.Bridges) == 0),
		"%s: a device on the root bus has no parent or bridges", where)
	pr.check(d.RootPort == "" || inDomain(bdf, d.RootPort, domain),
		"%s: rootPort %q is not a PCI address in %s", where, d.RootPort, d.RootComplex)
	if d.RootPortID != nil {
		checkPCIID(pr, where+": rootPortID", *d.RootPortID)
	}
	pr.check(len(d.Bridges) == 0 || p.Spec.SwitchPort != nil,
		"%s: bridges are declared, but profile %s has no switchPort", where, p.Name)

	parents := map[string]bool{d.RootPort: true}
	for _, b := range d.Bridges {
		pr.check(inDomain(bdf, b.Address, domain), "%s: bridge %q is not a PCI address in %s", where, b.Address, d.RootComplex)
		pr.check(parents[b.Parent], "%s: bridge %s: parent %q is not the root port or an earlier bridge", where, b.Address, b.Parent)
		parents[b.Address] = true
		book.claim(b.Address, owner)
	}
	pr.check(inDomain(deviceAddr, d.Address, domain), "%s: address %q is not DDDD:BB:DD in %s", where, d.Address, d.RootComplex)
	pr.check(parents[d.Parent], "%s: parent %q is not the root port or a bridge", where, d.Parent)
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
