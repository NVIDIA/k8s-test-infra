// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nicdiff

import (
	"path"
	"sort"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
)

// The capture files the relations are derived from. A relation names every
// file it reads, so a side that lacks any of them makes it a capture blind
// spot rather than a difference.
const (
	srcRDMA = "rdma-sysfs.txt"
	srcPCI  = "pci-nic-sysfs.txt"
	srcAll  = "pci-all-functions.txt"
	srcVPD  = "pci-vpd-fields.txt"
	srcNet  = "net-sysfs.txt"
	srcChar = "rdma-char-devices.txt"
)

var (
	fromRDMA       = []string{srcRDMA}
	fromVF         = []string{srcRDMA, srcPCI}
	fromVPD        = []string{srcVPD}
	fromBoard      = []string{srcVPD, srcPCI}
	fromDataDirect = []string{srcVPD, srcPCI, srcAll}
	fromNet        = []string{srcNet}
	fromChar       = []string{srcChar}
)

// node is a node's NIC surfaces with the relations between its records added.
type node struct{ *nicsurfaces.Node }

func (c node) objects(kind string) []string               { return c.Objects(kind) }
func (c node) get(obj, attr string) (string, bool)        { return c.Get(obj, attr) }
func (c node) set(srcs []string, obj, attr, value string) { c.Set(obj, attr, value, srcs...) }

// Identifiers (GUIDs, MACs, serials, VPD IDs, switch IDs) differ between any
// two nodes, so they are compared through what they relate, not by value. Each
// relation becomes a "rel/<name>/<subject>" object whose value is compared
// like any other. nic-surfaces records the relations inside one value, such as
// GID 0 carrying the port GUID; these span objects. A scrub keeps both, since
// it gives equal values the same placeholder.
func (c node) addRelations() {
	c.relateSMI()
	c.relateGUIDs()
	c.relateVUSerial()
	c.relateBoards()
	c.relateDataDirect()
	c.relateSwitchIDs()
	c.relateDeviceNodes()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// relateSMI: an SMI plane device shares its parent's node GUID, and each of its
// ports the parent port's LID.
func (c node) relateSMI() {
	for _, o := range c.objects("rdma") {
		bdf, role := path.Split(strings.TrimPrefix(o, "rdma/"))
		if role != "smi" {
			continue
		}
		parent := "rdma/" + bdf + "dev"
		g, _ := c.get(o, "node_guid")
		pg, ok := c.get(parent, "node_guid")
		c.set(fromRDMA, "rel/smi_node_guid/"+strings.TrimSuffix(bdf, "/"), "is_parent_node_guid", yesNo(ok && g == pg))
	}
	for _, o := range c.objects("port") {
		key := strings.TrimPrefix(o, "port/")
		dir, port := path.Split(key)
		bdf, role := path.Split(strings.TrimSuffix(dir, "/"))
		if role != "smi" {
			continue
		}
		lid, _ := c.get(o, "lid")
		plid, ok := c.get("port/"+bdf+"dev/1", "lid")
		c.set(fromRDMA, "rel/smi_lid/"+bdf+port, "is_parent_lid", yesNo(ok && lid == plid))
	}
}

// relateGUIDs: whose node GUID each device's system image GUID is, a VF
// shares its PF's system image GUID, and no two devices share a node GUID
// except an SMI device with its parent.
func (c node) relateGUIDs() {
	var devices []string
	owner := map[string]string{} // node GUID -> the function of its device
	for _, o := range c.objects("rdma") {
		if !strings.HasSuffix(o, "/smi") {
			devices = append(devices, o)
			if g, ok := c.get(o, "node_guid"); ok {
				owner[g] = strings.TrimSuffix(strings.TrimPrefix(o, "rdma/"), "/dev")
			}
		}
	}
	seen := map[string]int{}
	for _, o := range devices {
		bdf := strings.TrimSuffix(strings.TrimPrefix(o, "rdma/"), "/dev")
		g, okG := c.get(o, "node_guid")
		s, okS := c.get(o, "sys_image_guid")
		if okS {
			c.set(fromRDMA, "rel/sys_image_guid/"+bdf, "is_node_guid_of", guidOwner(owner, s, bdf))
		}
		if okG {
			seen[g]++
		}
		c.relateVF(bdf, s, okS)
	}
	if len(seen) > 0 {
		distinct := true
		for _, n := range seen {
			distinct = distinct && n == 1
		}
		c.set(fromRDMA, "rel/node_guids", "distinct", yesNo(distinct))
	}
}

// guidOwner names the device whose node GUID guid is: self for the device on
// bdf, another device's function, or none.
func guidOwner(owner map[string]string, guid, bdf string) string {
	switch o, ok := owner[guid]; {
	case !ok:
		return "none"
	case o == bdf:
		return "self"
	default:
		return o
	}
}

// relateVUSerial: whether a function's VPD VU field starts with its serial
// number, as a BlueField's does; a ConnectX-8 writes a separate ID there.
func (c node) relateVUSerial() {
	for _, o := range c.objects("pci") {
		vu, okU := c.get(o, "vpd.VU")
		sn, okS := c.get(o, "vpd.SN")
		if okU && okS && sn != "" {
			c.set(fromVPD, "rel/vu_serial/"+strings.TrimPrefix(o, "pci/"), "starts_with_serial", yesNo(strings.HasPrefix(vu, sn)))
		}
	}
}

// relateVF records whether the device on function bdf, if it is a VF, shares
// its PF's system image GUID.
func (c node) relateVF(bdf, sysImage string, ok bool) {
	pf, vf := c.get("pci/"+bdf, "physfn")
	if !vf {
		return
	}
	ps, okP := c.get("rdma/"+pf+"/dev", "sys_image_guid")
	c.set(fromVF, "rel/vf_sys_image_guid/"+bdf, "is_pf_sys_image_guid", yesNo(ok && okP && sysImage == ps))
}

// networkFunctions returns the NIC's network PFs: Ethernet or InfiniBand
// functions that are not virtual functions.
func (c node) networkFunctions() []string {
	var out []string
	for _, o := range c.objects("pci") {
		class, _ := c.get(o, "class")
		if _, vf := c.get(o, "physfn"); vf || !(strings.HasPrefix(class, "0x0200") || strings.HasPrefix(class, "0x0207")) {
			continue
		}
		out = append(out, strings.TrimPrefix(o, "pci/"))
	}
	return out
}

// relateBoards: the ASICs that share a board serial number, each named by its
// first network function. On GB300 a dual-ASIC board carries two.
func (c node) relateBoards() {
	var pfs []string
	for _, pf := range c.networkFunctions() {
		if strings.HasSuffix(pf, ".0") {
			pfs = append(pfs, pf)
		}
	}
	for _, pf := range pfs {
		sn, ok := c.get("pci/"+pf, "vpd.SN")
		if !ok {
			continue
		}
		var partners []string
		for _, other := range pfs {
			if osn, _ := c.get("pci/"+other, "vpd.SN"); other != pf && osn == sn {
				partners = append(partners, other)
			}
		}
		c.set(fromBoard, "rel/board/"+pf, "shares_serial_with", strings.Join(partners, ","))
	}
}

// relateDataDirect pairs each network PF with the Data Direct function whose
// VPD V3 (the ASIC's UUID) it shares, as rdma-core's rdma_topo does, and with
// the GPU under that function's root port. The pairing by the VU field is kept
// beside it, since earlier evidence used it.
func (c node) relateDataDirect() {
	var dds []string
	for _, o := range c.objects("pci") {
		if d, _ := c.get(o, "device"); d == "0x2100" {
			dds = append(dds, strings.TrimPrefix(o, "pci/"))
		}
	}
	if len(dds) == 0 {
		return
	}
	gpuOn := map[string]string{}
	for _, o := range c.objects("gpu") {
		root, _ := c.get(o, "root")
		gpuOn[root] = strings.TrimPrefix(o, "gpu/")
	}
	for _, pf := range c.networkFunctions() {
		if v3, ok := c.get("pci/"+pf, "vpd.V3"); ok && strings.HasSuffix(pf, ".0") {
			c.pairDataDirect(pf, v3, dds, gpuOn)
		}
	}
}

// pairDataDirect records the Data Direct function among dds that pairs with
// network PF pf by V3, the GPU on its root port, and the one VU pairs it with.
func (c node) pairDataDirect(pf, v3 string, dds []string, gpuOn map[string]string) {
	byV3, byVU, gpu := "none", "none", "none"
	vu := vuID(c.vpdVU(pf))
	for _, dd := range dds {
		if dv3, _ := c.get("pci/"+dd, "vpd.V3"); dv3 == v3 {
			byV3 = dd
			root, _ := c.get("pci/"+dd, "root")
			if g, ok := gpuOn[root]; ok {
				gpu = g
			}
		}
		if vu != "" && vuID(c.vpdVU(dd)) == vu {
			byVU = dd
		}
	}
	c.set(fromDataDirect, "rel/data_direct/"+pf, "by_v3", byV3)
	c.set(fromDataDirect, "rel/data_direct/"+pf, "gpu", gpu)
	c.set(fromDataDirect, "rel/data_direct/"+pf, "by_vu", byVU)
}

func (c node) vpdVU(bdf string) string {
	v, _ := c.get("pci/"+bdf, "vpd.VU")
	return v
}

// vuID is the identifier part of a VU field: everything before the MLNX
// marker that says which kind of function holds it.
func vuID(vu string) string {
	id, _, _ := strings.Cut(vu, "MLNX")
	return id
}

// vuKind is the marker part of a VU field: MLNXS0D0F0 on a network function,
// MLNXDDIS0D0F0 on a Data Direct function.
func vuKind(vu string) string {
	if i := strings.Index(vu, "MLNX"); i >= 0 {
		return vu[i:]
	}
	return ""
}

// relateSwitchIDs: the functions whose interfaces share a switch ID, which on
// a switchdev NIC are the ports of one eswitch.
func (c node) relateSwitchIDs() {
	nets := c.objects("net")
	for _, o := range nets {
		id, ok := c.get(o, "phys_switch_id")
		if !ok || id == "" {
			continue
		}
		bdf, _, _ := strings.Cut(strings.TrimPrefix(o, "net/"), "/")
		set := map[string]bool{}
		for _, other := range nets {
			obdf, _, _ := strings.Cut(strings.TrimPrefix(other, "net/"), "/")
			if oid, _ := c.get(other, "phys_switch_id"); oid == id && obdf != bdf {
				set[obdf] = true
			}
		}
		partners := make([]string, 0, len(set))
		for p := range set {
			partners = append(partners, p)
		}
		sort.Strings(partners)
		c.set(fromNet, "rel/switch_id/"+strings.TrimPrefix(o, "net/"), "shared_with", strings.Join(partners, ","))
	}
}

// relateDeviceNodes: each verbs and MAD device node carries the device number
// its sysfs entry gives.
func (c node) relateDeviceNodes() {
	for _, kind := range []string{"uverbs", "mad"} {
		for _, o := range c.objects(kind) {
			node, ok := c.get(o, "devnode")
			if !ok {
				continue
			}
			dev, _ := c.get(o, "dev")
			number, _, _ := strings.Cut(node, " ")
			c.set(fromChar, "rel/devnode/"+o, "matches_sysfs", yesNo(number == dev))
		}
	}
}
