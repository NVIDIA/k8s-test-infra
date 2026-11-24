// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"fmt"
	"maps"
	"slices"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

var (
	technologies  = []v1alpha1.NICTechnology{v1alpha1.NICTechnologyIB, v1alpha1.NICTechnologyRoCE, v1alpha1.NICTechnologyEFA}
	vpdScopes     = []v1alpha1.VPDScope{v1alpha1.VPDScopeBoard, v1alpha1.VPDScopeASIC, v1alpha1.VPDScopeBoardPosition}
	rdmaOptionals = []v1alpha1.RDMAAttribute{"fw_ver", "hca_type", "board_id", "hw_rev"}
)

// ValidateProfile checks a profile's values and the references between its
// parts.
func ValidateProfile(p *v1alpha1.SNICProfile) error {
	s := &p.Spec
	var pr problems
	validateIdentity(&pr, s)
	validateVPD(&pr, s.VPD)
	pr.check(len(s.FunctionTypes) > 0, "no functionTypes")
	for _, name := range slices.Sorted(maps.Keys(s.FunctionTypes)) {
		validateFunctionType(&pr, name, s.FunctionTypes)
	}
	validateConfigurations(&pr, s)
	return pr.err()
}

func validateIdentity(pr *problems, s *v1alpha1.SNICProfileSpec) {
	id := s.Identity
	pr.check(id.Model != "", "identity.model is empty")
	pr.check(optional(id.HWRev, hexValue), "identity.hwRev %q is not 0x and hex digits", id.HWRev)
	pr.check(optional(id.VendorOUI, lowerHex), "identity.vendorOUI %q is not 0x and lowercase hex digits", id.VendorOUI)
	if s.SwitchPort != nil {
		checkPCIID(pr, "switchPort", *s.SwitchPort)
	}
}

func validateVPD(pr *problems, v *v1alpha1.NICVPD) {
	if v == nil {
		return
	}
	for _, k := range slices.Sorted(maps.Keys(v.Identities)) {
		scope := v.Identities[k]
		// A scope may name another identity keyword whose value this one
		// reuses, but only one with a scope of its own, so there's no chain.
		ref, isRef := v.Identities[string(scope)]
		pr.check(slices.Contains(vpdScopes, scope) || (isRef && string(scope) != k && slices.Contains(vpdScopes, ref)),
			"vpd.identities.%s: scope %q is neither board, asic nor boardPosition, nor an identity keyword with one of them", k, scope)
		_, both := v.Keywords[k]
		pr.check(!both, "vpd keyword %s is both a model value and an identity", k)
	}
}

func validateFunctionType(pr *problems, name string, types map[string]v1alpha1.NICFunctionType) {
	ft := types[name]
	where := "functionTypes." + name
	checkPCIID(pr, where, v1alpha1.PCIID{VendorID: ft.VendorID, DeviceID: ft.DeviceID})
	// Either subsystem ID may be left out, and is then 0000.
	pr.check(optional(ft.SubsystemVendorID, hexID), "%s: subsystemVendorID %q is not four lowercase hex digits", where, ft.SubsystemVendorID)
	pr.check(optional(ft.SubsystemDeviceID, hexID), "%s: subsystemDeviceID %q is not four lowercase hex digits", where, ft.SubsystemDeviceID)
	pr.check(ft.Driver != "", "%s: driver is empty", where)
	pr.check(optional(ft.Revision, revision), "%s: revision %q is not 0x and two hex digits", where, ft.Revision)
	if l := ft.PCIeLink; l != nil {
		pr.check(l.Speed != "" && l.Width >= 0 && l.MaxWidth >= 0,
			"%s: pcieLink needs a speed and widths that aren't negative", where)
	}
	validateRDMAAttributes(pr, where, &ft)
	pr.check(ft.TotalVFs >= 0, "%s: totalVFs is negative", where)
	if ft.VFType != "" {
		_, known := types[ft.VFType]
		pr.check(known && ft.VFType != name, "%s: vfType %q is not another function type", where, ft.VFType)
		pr.check(ft.TotalVFs > 0, "%s: vfType needs totalVFs", where)
	}
}

func validateRDMAAttributes(pr *problems, where string, ft *v1alpha1.NICFunctionType) {
	for _, a := range slices.Concat(ft.RDMAOmit, ft.RDMAEmpty) {
		pr.check(slices.Contains(rdmaOptionals, a), "%s: RDMA attribute %q can't be omitted or empty", where, a)
	}
	for _, a := range ft.RDMAEmpty {
		pr.check(!slices.Contains(ft.RDMAOmit, a), "%s: %s is in both rdmaOmit and rdmaEmpty", where, a)
	}
}

func validateConfigurations(pr *problems, s *v1alpha1.SNICProfileSpec) {
	pr.check(len(s.Configurations) > 0, "no configurations")
	seen := map[string]bool{}
	for i := range s.Configurations {
		c := &s.Configurations[i]
		pr.check(c.Name != "" && !seen[c.Name], "configuration name %q is empty or used twice", c.Name)
		seen[c.Name] = true

		where := "configuration " + c.Name
		ports := validatePorts(pr, where, c.Ports)
		functions := validateFunctions(pr, where, c.Functions, ports, s.FunctionTypes)
		validatePortOwners(pr, where, c)
		validateRDMADevices(pr, where, c.RDMADevices, ports, functions)
	}
	pr.check(seen[s.DefaultConfiguration], "defaultConfiguration %q is not a configuration", s.DefaultConfiguration)
}

// validatePorts checks a configuration's ports and returns their IDs.
func validatePorts(pr *problems, where string, ports []v1alpha1.NICPort) map[string]bool {
	ids := map[string]bool{}
	for _, port := range ports {
		pr.check(port.ID != "" && !ids[port.ID], "%s: port ID %q is empty or used twice", where, port.ID)
		ids[port.ID] = true
		_, knownSpeed := laneGbps[port.LaneSpeed]
		pr.check(slices.Contains(technologies, port.Technology), "%s: port %s: unknown technology %q", where, port.ID, port.Technology)
		pr.check(knownSpeed, "%s: port %s: unknown laneSpeed %q", where, port.ID, port.LaneSpeed)
		pr.check(port.Width > 0, "%s: port %s: width must be positive", where, port.ID)
		pr.check(port.Planes >= 0, "%s: port %s: planes must not be negative", where, port.ID)
		pr.check(optional(port.CapMask, capMask), "%s: port %s: capMask %q is not 0x and eight hex digits", where, port.ID, port.CapMask)
		pr.check(port.LMC >= 0 && port.LMC <= 7, "%s: port %s: lmc %d is out of range", where, port.ID, port.LMC)
	}
	return ids
}

// validateFunctions checks a configuration's functions and returns the ports
// each owns, by function ID.
func validateFunctions(pr *problems, where string, functions []v1alpha1.NICFunction, ports map[string]bool,
	types map[string]v1alpha1.NICFunctionType) map[string][]string {
	pr.check(len(functions) > 0, "%s: no functions", where)
	ids := map[string][]string{}
	numbers := map[string]bool{} // PCI device + "." + function number
	for _, f := range functions {
		_, dup := ids[f.ID]
		pr.check(f.ID != "" && !dup, "%s: function ID %q is empty or used twice", where, f.ID)
		ids[f.ID] = f.Ports
		_, knownType := types[f.Type]
		pr.check(knownType, "%s: function %s: unknown type %q", where, f.ID, f.Type)
		pr.check(f.PCIDevice != "", "%s: function %s: pciDevice is empty", where, f.ID)
		pr.check(f.Function >= 0 && f.Function <= 7, "%s: function %s: function number %d is out of range", where, f.ID, f.Function)
		number := fmt.Sprintf("%s.%d", f.PCIDevice, f.Function)
		pr.check(!numbers[number], "%s: function %s: %s is used twice", where, f.ID, number)
		numbers[number] = true
		pr.check(classCode.MatchString(f.Class), "%s: function %s: class %q is not 0x and six hex digits", where, f.ID, f.Class)
		for _, port := range f.Ports {
			pr.check(ports[port], "%s: function %s: unknown port %q", where, f.ID, port)
		}
	}
	return ids
}

// validatePortOwners checks that every port has an owner, and at most one per
// PCI device: a NIC with several host links shows a port on one function per
// link.
func validatePortOwners(pr *problems, where string, c *v1alpha1.NICConfiguration) {
	owner := map[string]string{} // port + "/" + PCI device -> function
	owned := map[string]bool{}
	for _, f := range c.Functions {
		for _, port := range f.Ports {
			key := port + "/" + f.PCIDevice
			prev, taken := owner[key]
			pr.check(!taken, "%s: port %s is owned by %s and %s", where, port, prev, f.ID)
			owner[key] = f.ID
			owned[port] = true
		}
	}
	for _, port := range c.Ports {
		pr.check(owned[port.ID], "%s: port %s has no function", where, port.ID)
	}
}

// validateRDMADevices checks the RDMA devices against the ports each
// function owns: a device's ports are its function's, and a function has one
// device, as a PCI function has on hardware. An RDMA device's identifiers
// come from its function's position, so two on one function would share them.
func validateRDMADevices(pr *problems, where string, devices []v1alpha1.NICRDMADevice, ports map[string]bool,
	functions map[string][]string) {
	ids := map[string]bool{}
	onFunction := map[string]string{} // function -> RDMA device
	for _, d := range devices {
		pr.check(d.ID != "" && !ids[d.ID], "%s: RDMA device ID %q is empty or used twice", where, d.ID)
		ids[d.ID] = true
		prev, taken := onFunction[d.Function]
		pr.check(!taken, "%s: RDMA devices %s and %s are both on function %s, which has one", where, prev, d.ID, d.Function)
		onFunction[d.Function] = d.ID
		owned, known := functions[d.Function]
		pr.check(known, "%s: RDMA device %s: unknown function %q", where, d.ID, d.Function)
		pr.check(len(d.Ports) > 0, "%s: RDMA device %s has no ports", where, d.ID)
		listed := map[string]bool{}
		for _, port := range d.Ports {
			pr.check(!listed[port], "%s: RDMA device %s lists port %s twice", where, d.ID, port)
			listed[port] = true
			pr.check(ports[port], "%s: RDMA device %s: unknown port %q", where, d.ID, port)
			pr.check(!known || slices.Contains(owned, port),
				"%s: RDMA device %s: port %s doesn't belong to function %s", where, d.ID, port, d.Function)
		}
	}
}
