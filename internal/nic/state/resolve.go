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
	// Configurations are the configuration choices of the Node's rack group,
	// or the file-mode ones.
	Configurations []v1alpha1.NICConfigurationChoice
	// RDMA is the Node image's RDMA stack; nil when the declaration has none.
	RDMA *v1alpha1.RDMASoftware
	// Profiles are the NIC profiles, by name.
	Profiles declaration.Profiles
}

// Resolve validates a Node's NIC declaration and resolves it: each slot
// running the configuration a choice selects, else its profile's default,
// with identifiers derived from the Node's coordinates.
func Resolve(in Input) (*NICs, error) {
	if in.Topology == nil {
		return &NICs{RDMA: resolveRDMA(in.RDMA)}, nil
	}
	if err := validate(in); err != nil {
		return nil, err
	}
	configs, err := declaration.Select(in.Topology.NICs, in.Configurations, in.Profiles)
	if err != nil {
		return nil, err // validate already rejected this; kept so it can't go unseen
	}

	r := resolver{node: in.Node, out: &NICs{RDMA: resolveRDMA(in.RDMA)}, tree: newTree(in.Topology, in.Profiles)}
	r.ipoib = r.out.RDMA.IPoIB
	r.addTree(in.Topology)
	for i := range in.Topology.NICs {
		s := &in.Topology.NICs[i]
		r.addSlot(s, in.Profiles[s.Profile], configs[s.Slot])
	}
	r.finish()
	return r.out, nil
}

// validate checks the declaration and every profile its slots use, so the
// resolver can rely on each reference it follows.
func validate(in Input) error {
	errs := []error{
		declaration.ValidateNode(in.Topology, in.Configurations, in.Profiles),
		declaration.ValidateRDMA(in.RDMA),
	}
	checked := map[string]bool{}
	for _, s := range in.Topology.NICs {
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
	tree tree
	// pending are the RDMA devices until every slot is known: names and
	// system image GUIDs depend on the order of all of them.
	pending []pendingDevice
	// systemImage is each slot's systemImageGUID setting.
	systemImage map[string]string
	// rdmaGUIDs are the node GUIDs of each slot's RDMA devices, by device ID,
	// for the systemImageGUID that names one.
	rdmaGUIDs map[string]map[string]string
	// ipoib is whether ib_ipoib loads, giving each InfiniBand port a netdev.
	ipoib bool
}

type pendingDevice struct {
	device RDMADevice
	prefix string
	// ports are the device's ports in the order it numbers them, and
	// physical their physical numbers, which its identifiers follow.
	ports    []v1alpha1.NICPort
	physical []int
	function *placedFunction
	slot     string
}

// placedFunction is a resolved function and where it sits in its slot.
type placedFunction struct {
	*Function
	// position is the function's place in the slot: its PCI device and
	// function number. Identifiers hash it, never the profile's IDs, so
	// renaming a function, port or RDMA device in a profile changes none.
	position []string
	// physical is each port's physical number, its position in the
	// configuration's ports, p0 first. A function's port list is a set, so
	// its order decides nothing.
	physical map[string]int
	// multifunction is whether its PCI device has more than one function.
	multifunction bool
	device        v1alpha1.NICDevicePlacement
}

// at is the path of what sits at the function: its port n, or its RDMA
// device.
func (f *placedFunction) at(what ...string) []string {
	return append(slices.Clone(f.position), what...)
}

// portPath is the path of the function's port with the configuration's ID
// portID: its physical number.
func (f *placedFunction) portPath(portID string) []string {
	return f.at("port", strconv.Itoa(f.physical[portID]))
}

// physicalNumbers maps each port of a configuration to its physical number.
func physicalNumbers(c *v1alpha1.NICConfiguration) map[string]int {
	out := make(map[string]int, len(c.Ports))
	for k, p := range c.Ports {
		out[p.ID] = k
	}
	return out
}

func (r *resolver) addSlot(s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, c *v1alpha1.NICConfiguration) {
	inst := Instance{
		Slot:            s.Slot,
		Profile:         s.Profile,
		Configuration:   c.Name,
		Model:           p.Spec.Identity.Model,
		FirmwareVersion: p.Spec.Identity.FirmwareVersion,
		BoardID:         p.Spec.Identity.BoardID,
		Board:           s.Board,
		GPUs:            slices.Sorted(slices.Values(s.GPUs)),
		SwitchID:        r.node.ID(s.Slot, "switch"),
	}
	r.out.Instances = append(r.out.Instances, inst)
	if r.systemImage == nil {
		r.systemImage = map[string]string{}
	}
	r.systemImage[s.Slot] = s.SystemImageGUID

	for _, d := range s.Devices {
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
	functions := map[string]*placedFunction{}
	for _, f := range c.Functions {
		functions[f.ID] = r.addFunction(&inst, s, p, c, f, perDevice[f.PCIDevice] > 1)
	}
	for _, d := range c.RDMADevices {
		r.addRDMADevice(&inst, p, c, d, functions[d.Function])
	}
}

func (r *resolver) addFunction(inst *Instance, s *v1alpha1.NICSlot, p *v1alpha1.SNICProfile, c *v1alpha1.NICConfiguration,
	f v1alpha1.NICFunction, multifunction bool) *placedFunction {
	ftp, _ := p.Spec.FunctionType(f.Type)
	dp, _ := s.Device(f.PCIDevice)
	ft, d := *ftp, *dp
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
		Parent:      d.Parent,
		NUMANode:    d.NUMANode,
		TotalVFs:    ft.TotalVFs,
		Link:        ft.PCIeLink.DeepCopy(),
	}
	fn.RootPort, fn.RootPortID, fn.Upstream = r.tree.above(d.Parent)
	if ft.VFType != "" {
		vfp, _ := p.Spec.FunctionType(ft.VFType)
		vf := *vfp
		fn.VF = &VFType{PCI: PCIID{VendorID: vf.VendorID, DeviceID: vf.DeviceID}, Subsystem: subsystem(vf),
			Revision: vf.Revision, Driver: vf.Driver}
	}
	if ft.VPD {
		fn.VPD = r.vpd(s, p, &ft, d, f.Function)
	}
	r.out.Functions = append(r.out.Functions, *fn)
	placed := &placedFunction{
		Function:      fn,
		position:      []string{s.Slot, "device", f.PCIDevice, "function", strconv.Itoa(int(f.Function))},
		physical:      physicalNumbers(c),
		multifunction: multifunction,
		device:        d,
	}

	for _, portID := range f.Ports {
		k := slices.IndexFunc(c.Ports, func(port v1alpha1.NICPort) bool { return port.ID == portID })
		port := c.Ports[k]
		if port.Technology != v1alpha1.NICTechnologyRoCE {
			continue
		}
		ppn := "p" + strconv.Itoa(k)
		r.out.Netdevs = append(r.out.Netdevs, Netdev{
			Name:         predictableName("en", d.Address, f.Function, multifunction, "n"+ppn),
			Function:     fn.Address,
			Slot:         s.Slot,
			Port:         portID,
			LinkLayer:    LinkLayerEthernet,
			PhysPortName: ppn,
			Address:      r.node.MAC(placed.portPath(portID)...),
			SwitchID:     inst.SwitchID,
			Width:        port.Width,
			LaneSpeed:    port.LaneSpeed,
		})
	}
	return placed
}

// subsystem is a function type's subsystem IDs, each 0000 when left out.
func subsystem(ft v1alpha1.NICFunctionType) PCIID {
	return PCIID{VendorID: cmp.Or(ft.SubsystemVendorID, noSubsystem), DeviceID: cmp.Or(ft.SubsystemDeviceID, noSubsystem)}
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
		maps.Copy(out, s.VPD)
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
		// A keyword whose scope names another identity reuses that one's
		// generated value, as a BlueField's VU starts with its SN.
		source := k
		if _, ok := v.Identities[string(scope)]; ok {
			source = string(scope)
		}
		suffix := strings.ReplaceAll(ft.VPDSuffixes[k], "{function}", strconv.Itoa(int(function)))
		out[k] = r.node.ID(vpdPath(declaration.EffectiveScope(v, k), source, s, d)...) + suffix
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
	d v1alpha1.NICRDMADevice, fn *placedFunction) {
	ft, _ := p.Spec.FunctionType(fn.Type)
	id := p.Spec.Identity
	hwRev := cmp.Or(id.HWRev, defaultHWRev)
	dev := RDMADevice{
		Function:        fn.Address,
		Slot:            inst.Slot,
		ID:              d.ID,
		NodeType:        cmp.Or(ft.RDMANodeType, defaultNodeType),
		NodeGUID:        r.node.GUID(fn.at("rdma")...),
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
	// The device numbers its ports in the order the profile lists them, port
	// 1 first; their identifiers follow their physical numbers.
	physical := make([]int, 0, len(d.Ports))
	ports := make([]v1alpha1.NICPort, 0, len(d.Ports))
	for _, portID := range d.Ports {
		k := fn.physical[portID]
		physical = append(physical, k)
		ports = append(ports, c.Ports[k])
	}
	if r.rdmaGUIDs == nil {
		r.rdmaGUIDs = map[string]map[string]string{}
	}
	if r.rdmaGUIDs[inst.Slot] == nil {
		r.rdmaGUIDs[inst.Slot] = map[string]string{}
	}
	r.rdmaGUIDs[inst.Slot][d.ID] = dev.NodeGUID
	r.pending = append(r.pending, pendingDevice{
		device: dev, prefix: ft.RDMANamePrefix, ports: ports, physical: physical, function: fn, slot: inst.Slot,
	})
}

// finish orders everything by PCI address and resolves what depends on all
// of the Node's RDMA devices: their names, system image GUIDs and ports.
func (r *resolver) finish() {
	slices.SortFunc(r.out.Instances, func(a, b Instance) int { return strings.Compare(a.Slot, b.Slot) })
	slices.SortFunc(r.out.Bridges, func(a, b Bridge) int { return strings.Compare(a.Address, b.Address) })
	slices.SortFunc(r.out.Functions, func(a, b Function) int { return strings.Compare(a.Address, b.Address) })
	slices.SortStableFunc(r.out.Netdevs, compareNetdevs)
	netdevs := map[string]Netdev{} // function address + "/" + port ID
	for _, n := range r.out.Netdevs {
		netdevs[n.Function+"/"+n.Port] = n
	}

	// The kernel names RDMA devices in probe order, which follows PCI
	// order, each driver counting its own prefix from 0. A configuration
	// change re-probes every NIC of the node, so names are always assigned
	// from a cold start.
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
	// IPoIB netdevs come with their ports, so order all of them again.
	slices.SortStableFunc(r.out.Netdevs, compareNetdevs)
}

// compareNetdevs orders netdevs by function address, then port.
func compareNetdevs(a, b Netdev) int {
	return cmp.Or(strings.Compare(a.Function, b.Function), cmp.Compare(portIndex(a.PhysPortName), portIndex(b.PhysPortName)),
		strings.Compare(a.Name, b.Name))
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
		return r.rdmaGUIDs[slot][g]
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
			// The first port's GUID is the node GUID, as on hardware.
			port.GUID = pd.device.NodeGUID
			if k > 0 {
				port.GUID = r.node.GUID(pd.function.at("rdma", "port", strconv.Itoa(pd.physical[k]))...)
			}
			if r.ipoib {
				port.Netdev = r.addIPoIB(pd, k, p, port.GUID)
			}
			port.GIDs = []GID{{Index: 0, Address: "fe80:0000:0000:0000:" + port.GUID, Type: GIDTypeIBRoCEv1}}
		case v1alpha1.NICTechnologyRoCE:
			// GIDs 0 and 1 are the same link-local address from the netdev's
			// MAC, as RoCE v1 and as RoCE v2, both tied to the netdev.
			n := netdevs[pd.function.Address+"/"+p.ID]
			port.CapMask, port.LMC, port.Netdev = roceCapMask, 0, n.Name
			ll := linkLocalGID(n.Address)
			port.GIDs = []GID{
				{Index: 0, Address: ll, Type: GIDTypeIBRoCEv1, Netdev: n.Name},
				{Index: 1, Address: ll, Type: GIDTypeRoCEv2, Netdev: n.Name},
			}
		case v1alpha1.NICTechnologyEFA:
			// An EFA port has a link-local GID but no netdev to take it from;
			// it takes the MAC a netdev at its position would have.
			port.GIDs = []GID{{Index: 0, Address: linkLocalGID(r.node.MAC(pd.function.portPath(p.ID)...)), Type: GIDTypeIBRoCEv1}}
		}
		out = append(out, port)
	}
	return out
}

// addIPoIB adds the IPoIB netdev of the RDMA device's InfiniBand port k and
// returns its name: the predictable ib name of the function, with the port's
// dev_port after the first, and the 20-byte hardware address, whose last 16
// bytes are the port's GID. The leading queue pair number is the driver's and
// varies on hardware; 0 stands in for it, since nothing relates it to the
// declaration.
func (r *resolver) addIPoIB(pd *pendingDevice, k int, p v1alpha1.NICPort, guid string) string {
	suffix := ""
	if k > 0 {
		suffix = "d" + strconv.Itoa(k)
	}
	fn := pd.function
	n := Netdev{
		Name:      predictableName("ib", fn.device.Address, functionNumber(fn.Address), fn.multifunction, suffix),
		Function:  fn.Address,
		Slot:      pd.slot,
		Port:      p.ID,
		LinkLayer: LinkLayerInfiniBand,
		Address:   ipoibAddress(guid),
		Width:     p.Width,
		LaneSpeed: p.LaneSpeed,
	}
	r.out.Netdevs = append(r.out.Netdevs, n)
	return n.Name
}

// ipoibAddress is an IPoIB hardware address: a 4-byte queue pair number,
// here 0, then the 16-byte link-local GID ending in guid.
func ipoibAddress(guid string) string {
	hex := "00000000" + "fe80000000000000" + strings.ReplaceAll(guid, ":", "")
	parts := make([]string, 0, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		parts = append(parts, hex[i:i+2])
	}
	return strings.Join(parts, ":")
}

// functionNumber is the function number of a PCI address. Validation allows
// only 0 to 7, so the parse never fails here; bounding it to 32 bits keeps the
// conversion exact all the same.
func functionNumber(addr string) int32 {
	n, _ := strconv.ParseInt(addr[strings.LastIndex(addr, ".")+1:], 10, 32)
	return int32(n)
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

// predictableName is systemd's path-based name for a PCI netdev: the link
// type's prefix (en, ib), domain, bus and slot in decimal, the function only
// on a multi-function device, then suffix, such as the driver's physical port
// name.
func predictableName(prefix, deviceAddr string, function int32, multifunction bool, suffix string) string {
	parts := strings.Split(deviceAddr, ":")
	domain, _ := strconv.ParseUint(parts[0], 16, 32)
	bus, _ := strconv.ParseUint(parts[1], 16, 32)
	dev, _ := strconv.ParseUint(parts[2], 16, 32)
	var b strings.Builder
	b.WriteString(prefix)
	if domain != 0 {
		fmt.Fprintf(&b, "P%d", domain)
	}
	fmt.Fprintf(&b, "p%ds%d", bus, dev)
	if multifunction {
		fmt.Fprintf(&b, "f%d", function)
	}
	b.WriteString(suffix)
	return b.String()
}
