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

package nicsurfaces

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var smiName = regexp.MustCompile(`^smi[0-9]+$`)

// rdmaKey keys an RDMA device by its PCI function and its role, so a renamed
// device keeps its key: smi for a plane sub-device, dev otherwise. A device
// that is no PCI function is keyed unbound/<name>/<role>; diff nic pairs it
// with the other node's device of that name.
func rdmaKey(name, bdf string) string {
	role := "dev"
	if smiName.MatchString(name) {
		role = "smi"
	}
	if bdf == "" {
		return "unbound/" + name + "/" + role
	}
	return bdf + "/" + role
}

// keyOf returns an RDMA device's key, by name.
func (n *Node) keyOf(name string) string {
	if k, ok := n.rdmaKeys[name]; ok {
		return k
	}
	return rdmaKey(name, "")
}

var rdmaDeviceAttrs = []string{"node_type", "node_guid", "sys_image_guid", "fw_ver", "hca_type", "board_id", "hw_rev", "node_desc"}

// readRDMASysfs reads each RDMA device and its ports from rdma-sysfs.txt.
func (n *Node) readRDMASysfs(text string) {
	for _, b := range blocks(text) {
		dev, ports := splitPorts(b.lines)
		kv := keyValues(dev)
		bdf := lastBDF(kv["devpath"])
		key := rdmaKey(b.name, bdf)
		n.rdmaKeys[b.name] = key
		o := "rdma/" + key
		n.set(rdmaSysfs, o, "name", b.name)
		n.set(rdmaSysfs, o, "pci_function", yesNo(bdf != ""))
		for _, a := range rdmaDeviceAttrs {
			n.setSysfs(rdmaSysfs, o, a, kv, a)
		}
		n.set(rdmaSysfs, o, "files", kv["files"])
		n.set(rdmaSysfs, o, "ports", strconv.Itoa(len(ports)))
		guid, _ := n.get(o, "node_guid")
		for _, p := range ports {
			n.readPort("port/"+key+"/"+p.name, guid, p.lines)
		}
	}
}

// splitPorts separates a device's own lines from its "-- port N" sections.
func splitPorts(lines []string) ([]string, []block) {
	var dev []string
	var ports []block
	for _, l := range lines {
		if p, ok := strings.CutPrefix(l, "-- port "); ok {
			ports = append(ports, block{name: strings.TrimSpace(p)})
			continue
		}
		if len(ports) == 0 {
			dev = append(dev, l)
		} else {
			ports[len(ports)-1].lines = append(ports[len(ports)-1].lines, l)
		}
	}
	return dev, ports
}

var (
	portAttrs = []string{"state", "phys_state", "rate", "link_layer", "lid", "sm_lid", "sm_sl", "lid_mask_count", "cap_mask"}
	gidLine   = regexp.MustCompile(`^gid\[([0-9]+)\]=(\S*) type=(.*) ndev=(.*)$`)
)

type gid struct{ index, value, typ, ndev string }

func (n *Node) readPort(o, nodeGUID string, lines []string) {
	kv := keyValues(lines)
	for _, a := range portAttrs {
		n.setSysfs(rdmaSysfs, o, a, kv, a)
	}
	for _, a := range []string{"pkeys", "counters", "hw_counters", "files"} {
		n.set(rdmaSysfs, o, a, kv[a])
	}
	var gids []gid
	for _, l := range lines {
		if m := gidLine.FindStringSubmatch(l); m != nil {
			ndev, _ := sysfsValue(m[4])
			gids = append(gids, gid{m[1], m[2], m[3], ndev})
		}
	}
	values, types := make([]string, 0, len(gids)), make([]string, 0, len(gids))
	ndevs := 0
	for _, g := range gids {
		if !strings.HasSuffix(g.value, ":0000:0000:0000:0000") {
			values = append(values, g.value)
		}
		types = append(types, g.typ)
		if g.ndev != "" {
			ndevs++
		}
	}
	n.set(rdmaSysfs, o, "gids", sortedSet(values))
	n.set(rdmaSysfs, o, "gid_types", sortedSet(types))
	n.set(rdmaSysfs, o, "gid_ndevs", strconv.Itoa(ndevs))
	n.relateGIDs(o, nodeGUID, kv["link_layer"], gids, ndevs)
}

// relateGIDs records the relations inside a GID's value: on InfiniBand GID 0
// carries the port GUID, and a RoCE link-local GID carries its interface's MAC
// as an EUI-64. A scrub keeps both, as it builds a GID's placeholder from its
// GUID's or MAC's.
func (n *Node) relateGIDs(o, nodeGUID, linkLayer string, gids []gid, ndevs int) {
	for _, g := range gids {
		if g.index == "0" && linkLayer == "InfiniBand" {
			n.set(rdmaSysfs, o, "gid0_is_node_guid", yesNo(interfaceID(g.value) == nodeGUID))
		}
	}
	if ndevs == 0 {
		return
	}
	related := false
	for _, g := range gids {
		mac, ok := n.macs[g.ndev]
		if ok && strings.HasPrefix(g.value, "fe80:") && interfaceID(g.value) == eui64(mac) {
			related = true
		}
	}
	n.set(rdmaSysfs, o, "link_local_gid_from_mac", yesNo(related))
}

// interfaceID returns the last 64 bits of a GID in GUID form.
func interfaceID(gid string) string {
	parts := strings.Split(gid, ":")
	if len(parts) != 8 {
		return ""
	}
	return strings.Join(parts[4:], ":")
}

// eui64 turns a MAC into the interface ID of its link-local address.
func eui64(mac string) string {
	b := strings.Split(mac, ":")
	if len(b) != 6 {
		return ""
	}
	first, err := strconv.ParseUint(b[0], 16, 8)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%02x%s:%sff:fe%s:%s%s", first^2, b[1], b[2], b[3], b[4], b[5])
}

// relateIPoIB records whether each IPoIB interface's address ends in the GUID
// of an RDMA device on its PCI function, as the port's GID does.
func (n *Node) relateIPoIB() {
	for name, key := range n.netKeys {
		o := "net/" + key
		if t, _ := n.get(o, "type"); t != "32" {
			continue
		}
		addr, _ := n.get(o, "address")
		b := strings.Split(addr, ":")
		if len(b) != 20 {
			continue
		}
		tail := strings.Join([]string{b[12] + b[13], b[14] + b[15], b[16] + b[17], b[18] + b[19]}, ":")
		related := false
		for _, k := range n.rdmaKeys {
			guid, _ := n.get("rdma/"+k, "node_guid")
			if strings.HasPrefix(k, n.netFunction[name]+"/") && guid == tail {
				related = true
			}
		}
		n.set(netSysfs, o, "address_has_node_guid", yesNo(related))
	}
}

// devFile is one /dev/infiniband entry.
type devFile struct{ typ, dev, mode, target string }

// readCharDevices reads /dev/infiniband and the verbs and MAD classes behind
// it from rdma-char-devices.txt.
func (n *Node) readCharDevices(text string) {
	files := map[string]devFile{}
	for _, b := range blocks(text) {
		switch b.name {
		case "/dev/infiniband":
			files = devFiles(b.lines)
		case "/sys/class/infiniband_verbs":
			n.readClass(b.lines, "uverbs", "verbs_abi_version", files)
		case "/sys/class/infiniband_mad":
			n.readClass(b.lines, "mad", "mad_abi_version", files)
		}
	}
	var top []string
	for name := range files {
		if !strings.Contains(name, "/") {
			top = append(top, name)
		}
	}
	n.set(rdmaChar, "node", "dev_infiniband", sortedSet(top))
	for _, name := range []string{"rdma_cm", "mlx5_perm_ctrl_local", "mlx5_perm_ctrl_other_vhca"} {
		if v, ok := devnode(files, name); ok {
			n.set(rdmaChar, "devnode/"+name, "devnode", v)
		}
	}
}

func devFiles(lines []string) map[string]devFile {
	out := map[string]devFile{}
	for _, l := range lines {
		name, rest, _ := strings.Cut(l, " ")
		kv := keyValues(strings.Fields(rest))
		out[name] = devFile{kv["type"], kv["dev"], kv["mode"], kv["target"]}
	}
	return out
}

// devnode describes a device file as "major:minor mode" in decimal, or by its
// type when it is not a character device.
func devnode(files map[string]devFile, name string) (string, bool) {
	f, ok := files[name]
	if !ok {
		return "", false
	}
	if f.typ != "char" {
		return f.typ, true
	}
	major, minor, _ := strings.Cut(f.dev, ":")
	ma, err1 := strconv.ParseUint(major, 16, 32)
	mi, err2 := strconv.ParseUint(minor, 16, 32)
	if err1 != nil || err2 != nil {
		return f.dev + " " + f.mode, true
	}
	return fmt.Sprintf("%d:%d %s", ma, mi, f.mode), true
}

// readClass reads the verbs or MAD class: each node's RDMA device, port and
// device number, its file in /dev/infiniband, and for verbs nodes whether
// the PCI function links back to it and the udev links to it.
func (n *Node) readClass(lines []string, kind, abiAttr string, files map[string]devFile) {
	for _, l := range lines {
		if v, ok := strings.CutPrefix(l, "abi_version="); ok {
			n.set(rdmaChar, "node", abiAttr, v)
			continue
		}
		name, rest, _ := strings.Cut(l, " ")
		kv := keyValues(strings.Fields(rest))
		o := kind + "/" + n.keyOf(kv["ibdev"])
		if kind == "mad" {
			o += "/" + kv["port"] + "/" + strings.TrimRight(name, "0123456789")
		}
		n.set(rdmaChar, o, "name", name)
		n.set(rdmaChar, o, "dev", kv["dev"])
		if v, ok := devnode(files, name); ok {
			n.set(rdmaChar, o, "devnode", v)
		}
		if kind == "uverbs" {
			n.set(rdmaChar, o, "abi_version", kv["abi_version"])
			n.relateVerbsNode(o, name, kv, files)
		}
	}
}

func (n *Node) relateVerbsNode(o, name string, kv map[string]string, files map[string]devFile) {
	switch kv["backlink"] {
	case "none", "":
		n.set(rdmaChar, o, "function_backlink", "missing")
	default:
		n.set(rdmaChar, o, "function_backlink", yesNo(kv["backlink"] == kv["dev"]))
	}
	if f, ok := files["by-ibdev/uverbs-"+kv["ibdev"]]; ok {
		n.set(rdmaChar, o, "by_ibdev", yesNo(baseName(f.target) == name))
	}
	if _, ok := files["by-path"]; ok {
		linked := false
		for p, f := range files {
			if strings.HasPrefix(p, "by-path/") && baseName(f.target) == name {
				linked = true
			}
		}
		n.set(rdmaChar, o, "by_path", yesNo(linked))
	}
}

func baseName(p string) string { return p[strings.LastIndex(p, "/")+1:] }

// readModules reads the NIC and RDMA kernel modules from sys-module-rdma.txt:
// loaded (yes, or builtin when the kernel has it built in), the version and
// each parameter.
func (n *Node) readModules(text string) {
	for _, b := range blocks(text) {
		o := "module/" + b.name
		kv := keyValues(b.lines)
		if kv["initstate"] == "live" {
			n.set(sysModules, o, "loaded", "yes")
		} else {
			n.set(sysModules, o, "loaded", "builtin")
		}
		if v, ok := kv["version"]; ok {
			n.set(sysModules, o, "version", v)
		}
		for k, v := range kv {
			if p, ok := strings.CutPrefix(k, "parameters/"); ok {
				n.set(sysModules, o, "param."+p, v)
			}
		}
	}
}

// readRDMATool reads rdma system show and rdma link show.
func (n *Node) readRDMATool(system, link string) {
	if s := strings.TrimSpace(system); s != "" {
		n.set(rdmaSystem, "node", "rdma_system", strings.Join(strings.Fields(s), " "))
	}
	for _, l := range strings.Split(link, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[0] != "link" {
			continue
		}
		o := "rdmalink/" + f[1]
		for i := 2; i+1 < len(f); i += 2 {
			switch f[i] {
			case "state", "physical_state", "subnet_prefix", "lmc":
				n.set(rdmaLink, o, f[i], f[i+1])
			}
		}
		n.set(rdmaLink, o, "netdev", yesNo(strings.Contains(" "+l+" ", " netdev ")))
	}
}

// readIBVDevinfo reads ibv_devinfo -v: one record per field, per device and
// port, leaving out the identifiers and GIDs.
func (n *Node) readIBVDevinfo(text string) {
	dev, port := "", ""
	for _, l := range strings.Split(text, "\n") {
		k, v, ok := colonValue(l)
		switch {
		case !ok:
			continue
		case k == "hca_id":
			dev, port = v, ""
			continue
		case k == "port" && dev != "":
			port = v
			continue
		case dev == "", strings.HasPrefix(k, "GID["), k == "node_guid", k == "sys_image_guid", k == "port_lid", k == "sm_lid":
			continue
		}
		attr := k
		if port != "" {
			attr = "port." + port + "." + k
		}
		n.set(ibvDevinfo, "ibv/"+dev, attr, v)
	}
}

var (
	ibstatCA   = regexp.MustCompile(`^CA '([^']+)'`)
	ibstatPort = regexp.MustCompile(`^\s+Port ([0-9]+):`)
	ibstatSkip = regexp.MustCompile(`GUID|lid|Firmware|Hardware`)
)

// readIBStat reads ibstat, leaving out the GUIDs and LIDs, and the versions
// rdma-sysfs.txt holds.
func (n *Node) readIBStat(text string) {
	dev, port := "", ""
	for _, l := range strings.Split(text, "\n") {
		if m := ibstatCA.FindStringSubmatch(l); m != nil {
			dev, port = m[1], ""
			continue
		}
		if m := ibstatPort.FindStringSubmatch(l); m != nil {
			port = m[1]
			continue
		}
		k, v, ok := colonValue(l)
		if !ok || dev == "" || ibstatSkip.MatchString(k) {
			continue
		}
		attr := strings.ReplaceAll(k, " ", "_")
		if port != "" {
			attr = "port." + port + "." + attr
		}
		n.set(ibstat, "ibstat/"+dev, attr, v)
	}
}

// keyByDevice renames the records tools print per RDMA device name
// (ibv/mlx5_0) to the key the device has in sysfs (ibv/0000:03:00.0/dev).
func (n *Node) keyByDevice() {
	var objs []string
	for o := range n.Records {
		switch Kind(o) {
		case "ibv", "ibstat", "rdmalink", "topo":
			objs = append(objs, o)
		}
	}
	sort.Strings(objs)
	for _, o := range objs {
		kind, rest, _ := strings.Cut(o, "/")
		name, tail, _ := strings.Cut(rest, "/")
		key, ok := n.rdmaKeys[name]
		if !ok {
			continue
		}
		renamed := kind + "/" + key
		if tail != "" {
			renamed += "/" + tail
		}
		for a, v := range n.Records[o] {
			n.set(n.source[o+"\t"+a], renamed, a, v)
			delete(n.source, o+"\t"+a)
		}
		delete(n.Records, o)
		delete(n.first, o)
	}
}
