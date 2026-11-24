// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package state

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
)

// Defaults the kernel applies where the declaration says nothing.
const (
	defaultUMADMode = "600"
	defaultNodeType = "1: CA"
	defaultHWRev    = "0x0"
	noSubsystem     = "0000"
	// roceCapMask is the port capability mask the kernel's RoCE support
	// reports for every RoCE port, whatever the NIC.
	roceCapMask = "0x00010000"
)

// Input is a Node's NIC declaration and where the Node sits.
type Input struct {
	Node Node
	// Topology holds the Node's GPU slots and NIC slots.
	Topology *v1alpha1.SGPUTopology
	// Overrides are the rack group's NIC overrides, or the file-mode ones.
	Overrides []v1alpha1.NICOverride
	// RDMA is the Node image's RDMA stack; nil when the declaration has none.
	RDMA *v1alpha1.RDMASoftware
	// Profiles are the NIC profiles, by name.
	Profiles declaration.Profiles
}

// Resolve validates a Node's NIC declaration and resolves it: the slots as
// the overrides leave them, each running its selected configuration (the
// override's, then the slot's, then the profile's default), with identifiers
// derived from the Node's coordinates.
func Resolve(in Input) (*NICs, error) {
	if in.Topology == nil {
		return &NICs{RDMA: resolveRDMA(in.RDMA)}, nil
	}
	if err := validate(in); err != nil {
		return nil, err
	}
	slots, err := declaration.Select(in.Topology.NICs, in.Overrides)
	if err != nil {
		return nil, err // validate already rejected this; kept so it can't go unseen
	}

	r := resolver{node: in.Node, out: &NICs{RDMA: resolveRDMA(in.RDMA)}}
	for i := range slots {
		p := in.Profiles[slots[i].Profile]
		c, _ := p.Spec.Configuration(slots[i].Configuration)
		r.addSlot(&slots[i], p, c)
	}
	r.finish()
	return r.out, nil
}

// validate checks the declaration and every profile its slots use, so the
// resolver can rely on each reference it follows.
func validate(in Input) error {
	errs := []error{
		declaration.ValidateNode(in.Topology, in.Overrides, in.Profiles),
		declaration.ValidateRDMA(in.RDMA),
	}
	slots, _ := declaration.Select(in.Topology.NICs, in.Overrides)
	checked := map[string]bool{}
	for _, s := range slots {
		p, ok := in.Profiles[s.Profile]
		if !ok || checked[s.Profile] {
			continue
		}
		checked[s.Profile] = true
		if err := declaration.ValidateProfile(p); err != nil {
			errs = append(errs, fmt.Errorf("SNICProfile %s: %w", s.Profile, err))
		}
	}
	return errors.Join(errs...)
}

func resolveRDMA(rdma *v1alpha1.RDMASoftware) RDMA {
	out := RDMA{UMADMode: defaultUMADMode}
	if rdma == nil {
		return out
	}
	out.UMAD, out.IPoIB, out.DriverVersion = rdma.UMAD, rdma.IPoIB, rdma.DriverVersion
	if rdma.UMADMode != "" {
		out.UMADMode = rdma.UMADMode
	}
	return out
}

type resolver struct {
	node Node
	out  *NICs
	// pending are the RDMA devices until every slot is known: names and
	// system image GUIDs depend on the order of all of them.
	pending []pendingDevice
	// systemImage is each slot's systemImageGUID setting.
	systemImage map[string]string
}

type pendingDevice struct {
	device   RDMADevice
	prefix   string
	ports    []v1alpha1.NICPort
	function *Function
	slot     string
}

func (r *resolver) addSlot(s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, c *v1alpha1.NICConfiguration) {
	inst := Instance{
		Slot:            s.Slot,
		Profile:         s.Profile,
		Configuration:   c.Name,
		Model:           p.Spec.Identity.Model,
		FirmwareVersion: cmp.Or(s.FirmwareVersion, p.Spec.Identity.FirmwareVersion),
		BoardID:         p.Spec.Identity.BoardID,
		Board:           s.Board,
		GPUs:            slices.Clone(s.GPUs),
		SwitchID:        r.node.ID(s.Slot, "switch"),
	}
	r.out.Instances = append(r.out.Instances, inst)
	if r.systemImage == nil {
		r.systemImage = map[string]string{}
	}
	r.systemImage[s.Slot] = s.SystemImageGUID

	for _, name := range slices.Sorted(maps.Keys(s.Devices)) {
		d := s.Devices[name]
		for _, b := range d.Bridges {
			r.out.Bridges = append(r.out.Bridges, Bridge{
				Address: b.Address, Parent: b.Parent, RootComplex: d.RootComplex, ID: *p.Spec.SwitchPort, Slot: s.Slot,
			})
		}
	}

	perDevice := map[string]int{}
	for _, f := range c.Functions {
		perDevice[f.PCIDevice]++
	}
	functions := map[string]*Function{}
	for _, f := range c.Functions {
		fn := r.addFunction(&inst, s, p, c, f, perDevice[f.PCIDevice] > 1)
		functions[f.ID] = fn
	}
	for _, d := range c.RDMADevices {
		r.addRDMADevice(&inst, p, c, d, functions[d.Function])
	}
}

func (r *resolver) addFunction(inst *Instance, s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, c *v1alpha1.NICConfiguration,
	f v1alpha1.NICFunction, multifunction bool) *Function {
	ft := p.Spec.FunctionTypes[f.Type]
	d := s.Devices[f.PCIDevice]
	fn := &Function{
		Address:     fmt.Sprintf("%s.%d", d.Address, f.Function),
		Slot:        s.Slot,
		ID:          f.ID,
		Type:        f.Type,
		PCI:         PCIID{VendorID: ft.VendorID, DeviceID: ft.DeviceID},
		Subsystem:   subsystem(ft),
		Class:       f.Class,
		Revision:    ft.Revision,
		Driver:      ft.Driver,
		RootComplex: d.RootComplex,
		RootPort:    d.RootPort,
		RootPortID:  d.RootPortID.DeepCopy(),
		Upstream:    upstream(d, p.Spec.SwitchPort),
		Parent:      d.Parent,
		NUMANode:    d.NUMANode,
		TotalVFs:    ft.TotalVFs,
		Link:        ft.PCIeLink.DeepCopy(),
	}
	if ft.VFType != "" {
		vf := p.Spec.FunctionTypes[ft.VFType]
		fn.VF = &VFType{PCI: PCIID{VendorID: vf.VendorID, DeviceID: vf.DeviceID}, Subsystem: subsystem(vf),
			Revision: vf.Revision, Driver: vf.Driver}
	}
	if ft.VPD {
		fn.VPD = r.vpd(s, p, &ft, d, f.Function)
	}
	r.out.Functions = append(r.out.Functions, *fn)

	for _, portID := range f.Ports {
		k := slices.IndexFunc(c.Ports, func(port v1alpha1.NICPort) bool { return port.ID == portID })
		port := c.Ports[k]
		if port.Technology != v1alpha1.NICTechnologyRoCE {
			continue
		}
		ppn := "p" + strconv.Itoa(k)
		r.out.Netdevs = append(r.out.Netdevs, Netdev{
			Name:         predictableName(d.Address, f.Function, multifunction, ppn),
			Function:     fn.Address,
			Slot:         s.Slot,
			Port:         portID,
			PhysPortName: ppn,
			MAC:          r.node.MAC(s.Slot, "function", f.ID, "port", portID),
			SwitchID:     inst.SwitchID,
			Width:        port.Width,
			LaneSpeed:    port.LaneSpeed,
		})
	}
	return fn
}

func subsystem(ft v1alpha1.NICFunctionType) PCIID {
	if ft.SubsystemVendorID == "" {
		return PCIID{VendorID: noSubsystem, DeviceID: noSubsystem}
	}
	return PCIID{VendorID: ft.SubsystemVendorID, DeviceID: ft.SubsystemDeviceID}
}

// upstream lists the NIC switch ports between a device's root port and the
// device, top first.
func upstream(d v1alpha1.NICDevicePlacement, switchPort *PCIID) []PCIID {
	parent := map[string]string{}
	for _, b := range d.Bridges {
		parent[b.Address] = b.Parent
	}
	var ids []PCIID
	for a := d.Parent; a != d.RootPort; a = parent[a] {
		if _, ok := parent[a]; !ok {
			break
		}
		ids = append(ids, *switchPort)
	}
	return ids
}

// vpd resolves a function's VPD: the model keywords with the slot's
// overrides, the product name ending in the device's position, the part
// number, and each identity keyword shared at its scope.
func (r *resolver) vpd(s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, ft *v1alpha1.NICFunctionType,
	d v1alpha1.NICDevicePlacement, function int32) map[string]string {
	out := map[string]string{}
	if pn := p.Spec.Identity.PartNumber; pn != "" {
		out["PN"] = pn
	}
	v := p.Spec.VPD
	if v == nil {
		return out
	}
	maps.Copy(out, v.Keywords)
	maps.Copy(out, s.VPD)
	if name, ok := out["name"]; ok && d.VPDPosition != "" {
		out["name"] = name + " " + d.VPDPosition
	}
	for k, scope := range v.Identities {
		if slices.Contains(ft.VPDOmit, k) {
			continue
		}
		suffix := strings.ReplaceAll(ft.VPDSuffixes[k], "{function}", strconv.Itoa(int(function)))
		out[k] = r.node.ID(vpdPath(scope, k, s, d)...) + suffix
	}
	return out
}

// vpdPath is where a VPD identity sits: on the board, the board position or
// the ASIC, so everything at one coordinate shares the value.
func vpdPath(scope v1alpha1.VPDScope, keyword string, s *v1alpha1.NICSlot, d v1alpha1.NICDevicePlacement) []string {
	board := ""
	if s.Board != nil {
		board = strconv.Itoa(int(*s.Board))
	}
	switch scope {
	case v1alpha1.VPDScopeBoard:
		return []string{"board", board, "vpd", keyword}
	case v1alpha1.VPDScopeBoardPosition:
		return []string{"board", board, "position", d.VPDPosition, "vpd", keyword}
	default:
		return []string{s.Slot, "vpd", keyword}
	}
}

func (r *resolver) addRDMADevice(inst *Instance, p *v1alpha1.SNICProfile, c *v1alpha1.NICConfiguration,
	d v1alpha1.NICRDMADevice, fn *Function) {
	ft := p.Spec.FunctionTypes[fn.Type]
	id := p.Spec.Identity
	hwRev := cmp.Or(id.HWRev, defaultHWRev)
	dev := RDMADevice{
		Function:        fn.Address,
		Slot:            inst.Slot,
		ID:              d.ID,
		NodeType:        cmp.Or(ft.RDMANodeType, defaultNodeType),
		NodeGUID:        r.node.GUID(inst.Slot, "rdma", d.ID),
		FirmwareVersion: inst.FirmwareVersion,
		HWRev:           hwRev,
		BoardID:         id.BoardID,
		VendorOUI:       id.VendorOUI,
		PartID:          ft.DeviceID,
		Attributes:      map[string]string{},
	}
	for _, a := range []struct {
		name  v1alpha1.RDMAAttribute
		value string
	}{
		{"fw_ver", inst.FirmwareVersion},
		{"hca_type", id.HCAType},
		{"board_id", id.BoardID},
		{"hw_rev", sysfsHWRev(hwRev)},
	} {
		switch {
		case slices.Contains(ft.RDMAOmit, a.name):
		case slices.Contains(ft.RDMAEmpty, a.name):
			dev.Attributes[string(a.name)] = ""
		default:
			dev.Attributes[string(a.name)] = a.value
		}
	}
	ports := make([]v1alpha1.NICPort, 0, len(d.Ports))
	for _, portID := range d.Ports {
		k := slices.IndexFunc(c.Ports, func(port v1alpha1.NICPort) bool { return port.ID == portID })
		ports = append(ports, c.Ports[k])
	}
	r.pending = append(r.pending, pendingDevice{device: dev, prefix: ft.RDMANamePrefix, ports: ports, function: fn, slot: inst.Slot})
}

// finish orders everything by PCI address and resolves what depends on all
// of the Node's RDMA devices: their names, system image GUIDs and ports.
func (r *resolver) finish() {
	slices.SortFunc(r.out.Bridges, func(a, b Bridge) int { return strings.Compare(a.Address, b.Address) })
	slices.SortFunc(r.out.Functions, func(a, b Function) int { return strings.Compare(a.Address, b.Address) })
	slices.SortStableFunc(r.out.Netdevs, func(a, b Netdev) int {
		return cmp.Or(strings.Compare(a.Function, b.Function), cmp.Compare(portIndex(a.PhysPortName), portIndex(b.PhysPortName)))
	})
	netdevs := map[string]Netdev{} // function address + "/" + port ID
	for _, n := range r.out.Netdevs {
		netdevs[n.Function+"/"+n.Port] = n
	}

	// The kernel names RDMA devices in probe order, which follows PCI
	// order, each driver counting its own prefix from 0.
	slices.SortStableFunc(r.pending, func(a, b pendingDevice) int { return strings.Compare(a.device.Function, b.device.Function) })
	next := map[string]int{}
	systemImage := map[string]string{}
	for i := range r.pending {
		pd := &r.pending[i]
		pd.device.Name = pd.prefix + strconv.Itoa(next[pd.prefix])
		next[pd.prefix]++
		if _, ok := systemImage[pd.slot]; !ok {
			systemImage[pd.slot] = r.systemImageGUID(pd.slot, pd.device.NodeGUID)
		}
		pd.device.SystemImageGUID = systemImage[pd.slot]
		pd.device.Ports = r.ports(pd, netdevs)
		r.out.Devices = append(r.out.Devices, pd.device)
	}
}

// systemImageGUID is a slot's system image GUID: the node GUID of its
// lowest-addressed RDMA device, of the device the slot names, or a GUID of its
// own.
func (r *resolver) systemImageGUID(slot, lowest string) string {
	switch g := r.systemImage[slot]; g {
	case "":
		return lowest
	case v1alpha1.SystemImageGUIDDistinct:
		return r.node.GUID(slot, "system-image")
	default:
		return r.node.GUID(slot, "rdma", g)
	}
}

func (r *resolver) ports(pd *pendingDevice, netdevs map[string]Netdev) []Port {
	out := make([]Port, 0, len(pd.ports))
	for k, p := range pd.ports {
		port := Port{
			Number:     k + 1,
			ID:         p.ID,
			Technology: p.Technology,
			Width:      p.Width,
			LaneSpeed:  p.LaneSpeed,
			Planes:     p.Planes,
			CapMask:    p.CapMask,
			LMC:        p.LMC,
		}
		switch p.Technology {
		case v1alpha1.NICTechnologyIB:
			port.GID = "fe80:0000:0000:0000:" + pd.device.NodeGUID
		case v1alpha1.NICTechnologyRoCE:
			n := netdevs[pd.function.Address+"/"+p.ID]
			port.CapMask, port.LMC, port.Netdev, port.GID = roceCapMask, 0, n.Name, linkLocalGID(n.MAC)
		case v1alpha1.NICTechnologyEFA:
			// An EFA port has a link-local GID but no netdev to take it from.
			port.GID = linkLocalGID(r.node.MAC(pd.slot, "port", p.ID))
		}
		out = append(out, port)
	}
	return out
}

func portIndex(physPortName string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(physPortName, "p"))
	return n
}

// sysfsHWRev is hw_rev as RDMA sysfs shows it: the revision in hex without 0x
// or leading zeros.
func sysfsHWRev(rev string) string {
	return cmp.Or(strings.TrimLeft(strings.TrimPrefix(rev, "0x"), "0"), "0")
}

// linkLocalGID is the link-local GID of a MAC: fe80::/64 and the MAC's EUI-64.
func linkLocalGID(mac string) string {
	var b [6]uint64
	for i, part := range strings.Split(mac, ":") {
		b[i], _ = strconv.ParseUint(part, 16, 8)
	}
	return fmt.Sprintf("fe80:0000:0000:0000:%02x%02x:%02xff:fe%02x:%02x%02x", b[0]^2, b[1], b[2], b[3], b[4], b[5])
}

// predictableName is systemd's path-based name for a PCI netdev: domain, bus
// and slot in decimal, the function only on a multi-function device, then the
// driver's physical port name.
func predictableName(deviceAddr string, function int32, multifunction bool, physPortName string) string {
	parts := strings.Split(deviceAddr, ":")
	domain, _ := strconv.ParseUint(parts[0], 16, 32)
	bus, _ := strconv.ParseUint(parts[1], 16, 32)
	dev, _ := strconv.ParseUint(parts[2], 16, 32)
	var b strings.Builder
	b.WriteString("en")
	if domain != 0 {
		fmt.Fprintf(&b, "P%d", domain)
	}
	fmt.Fprintf(&b, "p%ds%d", bus, dev)
	if multifunction {
		fmt.Fprintf(&b, "f%d", function)
	}
	b.WriteString("n" + physPortName)
	return b.String()
}
