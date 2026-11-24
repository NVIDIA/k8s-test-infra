// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package conformance

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/state"
)

// records is a declared node's NIC surfaces, each record with the capture
// file a real node shows it in, as `mokka-capture nic-surfaces` lists them.
type records struct{ *nicsurfaces.Node }

func (r records) Set(obj, attr, value string) {
	r.Node.Set(obj, attr, value, nicsurfaces.SourcesOf(obj, attr)...)
}

// view writes a Node's resolved NIC state as the records `mokka-capture
// nic-surfaces` lists for a captured node. It formats the state the way
// MEP-0016's render rules (G6 and G9 to G12) say a node shows it; the node
// agent's simulators render the same state into files.
func view(n *state.NICs, gpus v1alpha1.SGPUGPUs) records {
	r := records{nicsurfaces.New()}
	for _, g := range n.GPUs {
		o := "gpu/" + g.Address
		r.Set(o, "device", "0x"+gpus.PCI.DeviceID)
		r.Set(o, "numa_node", strconv.Itoa(int(g.NUMANode)))
		r.Set(o, "root", g.RootComplex)
	}
	for _, b := range n.Bridges {
		r.Set("bridge/"+b.Address, "device", "0x"+b.ID.DeviceID)
		r.Set("bridge/"+b.Address, "root", b.RootComplex)
	}
	for i := range n.Functions {
		viewFunction(r, &n.Functions[i])
	}
	for i := range n.Netdevs {
		if n.Netdevs[i].LinkLayer == state.LinkLayerEthernet {
			viewNetdev(r, n, &n.Netdevs[i])
		}
	}
	var files deviceFiles
	for i := range n.Devices {
		viewDevice(r, n, i, &files)
	}
	viewNode(r, n, files)
	viewTopo(r, n)
	return r
}

// viewTopo shows each RDMA device's row of `nvidia-smi topo -m`: how the PCIe
// tree and the NUMA nodes connect its function to each GPU.
func viewTopo(r records, n *state.NICs) {
	parents := map[string]string{}
	for _, b := range n.Bridges {
		parents[b.Address] = b.Parent
	}
	for _, d := range n.Devices {
		f := function(n, d.Function)
		for _, g := range n.GPUs {
			r.Node.Set("topo/"+d.Function+"/dev", fmt.Sprintf("GPU%d", g.Index), topoRelation(&f, &g, parents),
				nicsurfaces.SourceOf("topo/", ""))
		}
	}
}

// topoRelation is nvidia-smi's name for the path between a function and a
// GPU: NODE across root complexes of one NUMA node, SYS across NUMA nodes, and
// below one root complex PIX through one switch port above both, PXB through
// more, PHB through the root complex. No committed capture has a NIC's RDMA
// device and a GPU below one root complex yet, so only NODE and SYS are
// checked against hardware.
func topoRelation(f *state.Function, g *state.GPU, parents map[string]string) string {
	switch {
	case f.RootComplex == g.RootComplex:
		return belowRootComplex(f.Parent, g.Parent, parents)
	case f.NUMANode == g.NUMANode:
		return "NODE"
	default:
		return "SYS"
	}
}

// belowRootComplex relates two devices below one root complex by the switch
// ports above them, given each switch port's parent.
func belowRootComplex(a, b string, parents map[string]string) string {
	above := func(addr string) []string {
		var out []string
		for p := addr; p != ""; p = parents[p] {
			out = append(out, p)
		}
		return out
	}
	bp := above(b)
	for i, port := range above(a) {
		_, isSwitchPort := parents[port]
		j := slices.Index(bp, port)
		switch {
		case j < 0:
		case !isSwitchPort:
			return "PHB"
		case i == 0 && j == 0:
			return "PIX"
		default:
			return "PXB"
		}
	}
	return "PHB"
}

func viewFunction(r records, f *state.Function) {
	o := "pci/" + f.Address
	r.Set(o, "vendor", "0x"+f.PCI.VendorID)
	r.Set(o, "device", "0x"+f.PCI.DeviceID)
	r.Set(o, "subsystem_vendor", "0x"+f.Subsystem.VendorID)
	r.Set(o, "subsystem_device", "0x"+f.Subsystem.DeviceID)
	r.Set(o, "class", f.Class)
	r.Set(o, "driver", f.Driver)
	r.Set(o, "numa_node", strconv.Itoa(int(f.NUMANode)))
	r.Set(o, "root", f.RootComplex)
	// The IDs above a function are known on the root bus, where there are
	// none, and where the root port's IDs are declared.
	if f.RootPort == "" || f.RootPortID != nil {
		var ids []string
		if f.RootPortID != nil {
			ids = append(ids, pciID(*f.RootPortID))
		}
		for _, id := range f.Upstream {
			ids = append(ids, pciID(id))
		}
		r.Set(o, "upstream", strings.Join(ids, ","))
	}
	if f.TotalVFs > 0 {
		r.Set(o, "sriov_totalvfs", strconv.Itoa(int(f.TotalVFs)))
		r.Set(o, "sriov_numvfs", "0")
	}
	r.Set(o, "virtfn_count", "0")
	r.Set(o, "modalias", modalias(f))
	if f.Revision != "" {
		r.Set(o, "revision", f.Revision)
	}
	if l := f.Link; l != nil {
		r.Set(o, "current_link_speed", l.Speed)
		r.Set(o, "max_link_speed", l.Speed)
		r.Set(o, "current_link_width", strconv.Itoa(int(l.Width)))
		maxWidth := l.Width
		if l.MaxWidth != 0 {
			maxWidth = l.MaxWidth
		}
		r.Set(o, "max_link_width", strconv.Itoa(int(maxWidth)))
	}
	for k, v := range f.VPD {
		r.Set(o, "vpd."+k, v)
	}
}

func viewNetdev(r records, n *state.NICs, nd *state.Netdev) {
	inst := instance(n, nd.Slot)
	f := function(n, nd.Function)
	o := "net/" + nd.Function + "/" + nd.PhysPortName
	r.Set(o, "name", nd.Name)
	r.Set(o, "type", "1")
	r.Set(o, "addr_len", "6")
	r.Set(o, "address", nd.Address)
	r.Set(o, "mtu", "1500")
	r.Set(o, "speed", strconv.Itoa(speedMbps(nd.Width, nd.LaneSpeed)))
	r.Set(o, "operstate", "up")
	r.Set(o, "carrier", "1")
	r.Set(o, "dev_port", "0")
	r.Set(o, "dev_id", "0x0")
	r.Set(o, "phys_port_name", nd.PhysPortName)
	r.Set(o, "phys_switch_id", nd.SwitchID)
	r.Set(o, "ethtool.driver", f.Driver)
	r.Set(o, "ethtool.firmware-version", fmt.Sprintf("%s (%s)", inst.FirmwareVersion, inst.BoardID))
	r.Set(o, "ethtool.bus-info", nd.Function)
}

// deviceFiles counts the umad and uverbs files and lists /dev/infiniband.
type deviceFiles struct {
	umad  int
	names []string
}

func viewDevice(r records, n *state.NICs, i int, files *deviceFiles) {
	d := &n.Devices[i]
	o := "rdma/" + d.Function + "/dev"
	r.Set(o, "name", d.Name)
	r.Set(o, "pci_function", "yes")
	r.Set(o, "node_type", d.NodeType)
	r.Set(o, "node_desc", "")
	r.Set(o, "node_guid", d.NodeGUID)
	r.Set(o, "sys_image_guid", d.SystemImageGUID)
	for a, v := range d.Attributes {
		r.Set(o, a, v)
	}
	r.Set(o, "ports", strconv.Itoa(len(d.Ports)))

	vo := "ibv/" + d.Function + "/dev"
	transport := "InfiniBand (0)"
	if len(d.Ports) > 0 && d.Ports[0].Technology == v1alpha1.NICTechnologyEFA {
		transport = "unspecified (4)"
	}
	r.Set(vo, "transport", transport)
	r.Set(vo, "fw_ver", d.FirmwareVersion)
	r.Set(vo, "vendor_id", d.VendorOUI)
	r.Set(vo, "vendor_part_id", decimal(d.PartID))
	r.Set(vo, "hw_ver", d.HWRev)
	if d.BoardID != "" {
		r.Set(vo, "board_id", d.BoardID)
	}
	r.Set(vo, "phys_port_cnt", strconv.Itoa(len(d.Ports)))
	for k := range d.Ports {
		viewPort(r, n, d, &d.Ports[k], files)
	}

	uo := "uverbs/" + d.Function + "/dev"
	r.Set(uo, "name", fmt.Sprintf("uverbs%d", i))
	r.Set(uo, "abi_version", "1")
	r.Set(uo, "dev", fmt.Sprintf("231:%d", 192+i))
	r.Set(uo, "devnode", fmt.Sprintf("231:%d 666", 192+i))
	files.names = append(files.names, fmt.Sprintf("uverbs%d", i))
}

func viewPort(r records, n *state.NICs, d *state.RDMADevice, p *state.Port, files *deviceFiles) {
	po := fmt.Sprintf("port/%s/dev/%d", d.Function, p.Number)
	vo := "ibv/" + d.Function + "/dev"
	pv := fmt.Sprintf("port.%d.", p.Number)
	r.Set(po, "state", "4: ACTIVE")
	r.Set(po, "phys_state", "5: LinkUp")
	r.Set(po, "rate", rateText(p.Width, p.LaneSpeed))
	r.Set(po, "link_layer", linkLayer(p.Technology))
	r.Set(vo, pv+"state", "PORT_ACTIVE (4)")
	r.Set(vo, pv+"max_mtu", "4096 (5)")
	r.Set(vo, pv+"port_lmc", fmt.Sprintf("0x%02x", p.LMC))
	r.Set(vo, pv+"link_layer", verbsLinkLayer(p.Technology))
	r.Set(vo, pv+"active_mtu", "4096 (5)")
	r.Set(po, "lid", "0x0")
	r.Set(po, "sm_lid", "0x0")
	r.Set(po, "sm_sl", "0")
	r.Set(po, "lid_mask_count", strconv.Itoa(int(p.LMC)))
	r.Set(po, "cap_mask", p.CapMask)
	r.Set(po, "pkeys", "0xffff")
	// The GID records as nic-surfaces derives them from the GID table: the
	// distinct addresses and types, and how many entries have a netdev.
	addrs, types := make([]string, 0, len(p.GIDs)), make([]string, 0, len(p.GIDs))
	ndevs := 0
	for _, g := range p.GIDs {
		addrs, types = append(addrs, g.Address), append(types, g.Type)
		if g.Netdev != "" {
			ndevs++
		}
	}
	slices.Sort(addrs)
	slices.Sort(types)
	r.Set(po, "gids", strings.Join(slices.Compact(addrs), ","))
	r.Set(po, "gid_types", strings.Join(slices.Compact(types), ","))
	r.Set(po, "gid_ndevs", strconv.Itoa(ndevs))
	switch p.Technology {
	case v1alpha1.NICTechnologyRoCE:
		r.Set(po, "link_local_gid_from_mac", "yes")
		r.Set(vo, pv+"active_mtu", roceActiveMTU(1500))
	case v1alpha1.NICTechnologyIB:
		r.Set(po, "gid0_is_node_guid", "yes")
		if p.Netdev != "" {
			viewIPoIB(r, n, d, p)
		}
	case v1alpha1.NICTechnologyEFA:
	}
	if n.RDMA.UMAD {
		mo := fmt.Sprintf("mad/%s/dev/%d/", d.Function, p.Number)
		r.Set(mo+"umad", "name", fmt.Sprintf("umad%d", files.umad))
		r.Set(mo+"umad", "dev", fmt.Sprintf("231:%d", files.umad))
		r.Set(mo+"umad", "devnode", fmt.Sprintf("231:%d %s", files.umad, n.RDMA.UMADMode))
		files.names = append(files.names, fmt.Sprintf("umad%d", files.umad))
		// A port with planes has no subnet-management interface of its own.
		if p.Technology == v1alpha1.NICTechnologyIB && p.Planes == 0 {
			r.Set(mo+"issm", "name", fmt.Sprintf("issm%d", files.umad))
			r.Set(mo+"issm", "dev", fmt.Sprintf("231:%d", 64+files.umad))
			r.Set(mo+"issm", "devnode", fmt.Sprintf("231:%d %s", 64+files.umad, n.RDMA.UMADMode))
			files.names = append(files.names, fmt.Sprintf("issm%d", files.umad))
		}
		files.umad++
	}
}

// viewIPoIB shows the IPoIB netdev the resolver gave an InfiniBand port, on
// the default partition, since a Mokka node has no subnet manager to set one.
func viewIPoIB(r records, n *state.NICs, d *state.RDMADevice, p *state.Port) {
	i := slices.IndexFunc(n.Netdevs, func(nd state.Netdev) bool { return nd.Name == p.Netdev })
	nd := n.Netdevs[i]
	no := fmt.Sprintf("net/%s/port%d/pkey0xffff", d.Function, p.Number-1)
	r.Set(no, "name", nd.Name)
	r.Set(no, "type", "32")
	r.Set(no, "addr_len", "20")
	r.Set(no, "address", nd.Address)
	r.Set(no, "mtu", "4092")
	r.Set(no, "speed", strconv.Itoa(speedMbps(p.Width, p.LaneSpeed)))
	r.Set(no, "operstate", "up")
	r.Set(no, "carrier", "1")
	r.Set(no, "dev_port", strconv.Itoa(p.Number-1))
	r.Set(no, "dev_id", "0x0")
	r.Set(no, "mode", "datagram")
	r.Set(no, "pkey", "0xffff")
	r.Set(no, "address_has_node_guid", "yes")
}

func viewNode(r records, n *state.NICs, files deviceFiles) {
	if len(n.Devices) > 0 {
		r.Set("devnode/rdma_cm", "devnode", "10:0 666")
		files.names = append(files.names, "rdma_cm")
	}
	slices.Sort(files.names)
	r.Set("node", "dev_infiniband", strings.Join(files.names, ","))
	r.Set("node", "verbs_abi_version", "6")
	r.Set("node", "mad_abi_version", "")
	if n.RDMA.UMAD {
		r.Set("node", "mad_abi_version", "5")
	}
}

func instance(n *state.NICs, slot string) state.Instance {
	i := slices.IndexFunc(n.Instances, func(in state.Instance) bool { return in.Slot == slot })
	return n.Instances[i]
}

func function(n *state.NICs, addr string) state.Function {
	i := slices.IndexFunc(n.Functions, func(f state.Function) bool { return f.Address == addr })
	return n.Functions[i]
}

func pciID(id state.PCIID) string { return "0x" + id.VendorID + ":0x" + id.DeviceID }

// modalias is the kernel's PCI modalias for a function.
func modalias(f *state.Function) string {
	hex := func(s string) uint64 { n, _ := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32); return n }
	c := hex(f.Class)
	return fmt.Sprintf("pci:v%08Xd%08Xsv%08Xsd%08Xbc%02Xsc%02Xi%02X",
		hex(f.PCI.VendorID), hex(f.PCI.DeviceID), hex(f.Subsystem.VendorID), hex(f.Subsystem.DeviceID),
		c>>16, (c>>8)&0xff, c&0xff)
}

func portGbps(width int32, speed v1alpha1.LaneSpeed) float64 {
	g, _ := declaration.LaneGbps(speed)
	return float64(width) * g
}

func speedMbps(width int32, speed v1alpha1.LaneSpeed) int { return int(portGbps(width, speed) * 1000) }

func rateText(width int32, speed v1alpha1.LaneSpeed) string {
	return fmt.Sprintf("%s Gb/sec (%dX %s)", strconv.FormatFloat(portGbps(width, speed), 'f', -1, 64), width, speed)
}

func linkLayer(t v1alpha1.NICTechnology) string {
	switch t {
	case v1alpha1.NICTechnologyIB:
		return "InfiniBand"
	case v1alpha1.NICTechnologyRoCE:
		return "Ethernet"
	default:
		return "Unknown"
	}
}

func verbsLinkLayer(t v1alpha1.NICTechnology) string {
	if t == v1alpha1.NICTechnologyEFA {
		return "Unspecified"
	}
	return linkLayer(t)
}

func decimal(hex string) string {
	n, _ := strconv.ParseUint(hex, 16, 32)
	return strconv.FormatUint(n, 10)
}

// roceActiveMTU is the largest IB MTU that fits a RoCE packet in the netdev
// MTU, as the kernel's iboe_get_mtu computes it.
func roceActiveMTU(netdevMTU int) string {
	for _, c := range []struct{ mtu, code int }{{4096, 5}, {2048, 4}, {1024, 3}, {512, 2}, {256, 1}} {
		if c.mtu <= netdevMTU-88 {
			return fmt.Sprintf("%d (%d)", c.mtu, c.code)
		}
	}
	return "invalid"
}
