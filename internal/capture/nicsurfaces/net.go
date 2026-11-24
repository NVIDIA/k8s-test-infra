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
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var netAttrs = []string{
	"type", "addr_len", "address", "mtu", "speed", "operstate", "carrier", "dev_port", "dev_id",
	"phys_port_name", "phys_switch_id", "mode", "pkey",
}

// readNetSysfs reads every interface's address, for relating GIDs to MACs,
// and each interface on a NIC function from net-sysfs.txt. An interface is
// keyed by its function and the port name the driver gives (p0, pf0vf0), or
// its device port when it has none, as an IPoIB interface does; an IPoIB
// interface also by its partition key, which tells a partition's child
// interface from its parent whatever either is named.
func (n *Node) readNetSysfs(text string) {
	bs := blocks(text)
	sort.Slice(bs, func(i, j int) bool { return bs[i].name < bs[j].name })
	seen := map[string]bool{}
	for _, b := range bs {
		kv := keyValues(b.lines)
		if a, ok := sysfsValue(kv["address"]); ok {
			n.macs[b.name] = a
		}
		bdf := lastBDF(kv["device"])
		if !n.nicFunctions[bdf] {
			continue
		}
		port, _ := sysfsValue(kv["phys_port_name"])
		if port == "" {
			devPort, _ := sysfsValue(kv["dev_port"])
			port = "port" + devPort
		}
		key := bdf + "/" + port
		if pkey, _ := sysfsValue(kv["pkey"]); pkey != "" {
			key += "/pkey" + pkey
		}
		if seen[key] {
			key += "#" + b.name
		}
		seen[key] = true
		n.netKeys[b.name], n.netFunction[b.name] = key, bdf
		o := "net/" + key
		n.set(netSysfs, o, "name", b.name)
		for _, a := range netAttrs {
			n.setSysfs(netSysfs, o, a, kv, a)
		}
		n.set(netSysfs, o, "files", kv["files"])
	}
}

// readECNQoS reads each NIC interface's ECN and QoS settings: the entries of
// its ecn and qos directories, whether ECN is enabled per priority on each
// side, and each QoS file's value.
func (n *Node) readECNQoS(text string) {
	for _, b := range blocks(text) {
		if key, ok := n.netKeys[b.name]; ok {
			n.readECNQoSBlock("net/"+key, b.lines)
		}
	}
}

func (n *Node) readECNQoSBlock(o string, lines []string) {
	entries := map[string][]string{} // ecn or qos -> its entries
	enable := map[string][]string{}  // ECN side -> per-priority enable
	for _, l := range lines {
		path, v, _ := strings.Cut(l, "=")
		parts := strings.Split(path, "/")
		if len(parts) < 2 || parts[0] != "ecn" && parts[0] != "qos" {
			continue
		}
		entries[parts[0]] = append(entries[parts[0]], parts[1])
		switch parts[0] + "/" + strconv.Itoa(len(parts)) {
		case "ecn/4":
			if parts[2] == "enable" {
				enable[parts[1]] = append(enable[parts[1]], v)
			}
		case "qos/2":
			n.set(netECNQoS, o, "qos."+parts[1], v)
		}
	}
	for dir, e := range entries {
		n.set(netECNQoS, o, dir, sortedSet(e))
	}
	for side, v := range enable {
		n.set(netECNQoS, o, "ecn."+side+".enable", strings.Join(v, ","))
	}
}

var (
	ethtoolSettings = map[string]bool{"Speed": true, "Duplex": true, "Lanes": true, "Port": true, "Auto-negotiation": true, "Link detected": true}
	ethtoolModule   = map[string]bool{
		"Identifier": true, "Connector": true, "Transceiver type": true, "Vendor name": true, "Vendor PN": true,
		"Vendor rev": true, "Length (SMF)": true, "Laser wavelength": true,
	}
	ethtoolKey = regexp.MustCompile(`^\s+[A-Za-z][^:]*:`)
)

// readEthtool reads ethtool -i, ethtool and ethtool -m for each NIC interface.
func (n *Node) readEthtool(info, settings, module string) {
	n.readColonFields(netEthtoolI, info, "ethtool.", func(k string) (string, bool) {
		return k, k == "driver" || k == "firmware-version" || k == "bus-info"
	})
	for _, b := range blocks(settings) {
		n.readEthtoolSettings(b)
	}
	n.readColonFields(netEthtoolM, module, "ethtool.module.", func(k string) (string, bool) {
		return attrName(k), ethtoolModule[k]
	})
}

// readColonFields sets, on each NIC interface's block, the "Key: value" lines
// whose key keep names, as prefix and the name keep gives.
func (n *Node) readColonFields(src, text, prefix string, keep func(string) (string, bool)) {
	for _, b := range blocks(text) {
		for _, l := range b.lines {
			k, v, ok := colonValue(l)
			if !ok {
				continue
			}
			if name, want := keep(k); want {
				n.setNet(src, b.name, prefix+name, v)
			}
		}
	}
}

func (n *Node) readEthtoolSettings(b block) {
	var modes []string
	inModes := false
	for _, l := range b.lines {
		k, v, ok := colonValue(l)
		if ok && k == "Supported link modes" {
			inModes, modes = true, appendModes(modes, v)
			continue
		}
		if ethtoolKey.MatchString(l) {
			inModes = false
		}
		if inModes {
			modes = appendModes(modes, l)
			continue
		}
		if ok && ethtoolSettings[k] {
			n.setNet(netEthtool, b.name, "ethtool."+attrName(k), v)
		}
	}
	if len(modes) > 0 {
		n.setNet(netEthtool, b.name, "ethtool.supported_link_modes", sortedSet(modes))
	}
}

// appendModes adds the link modes listed in text; "Not reported" is none.
func appendModes(modes []string, text string) []string {
	for _, m := range strings.Fields(text) {
		if m != "Not" && m != "reported" {
			modes = append(modes, m)
		}
	}
	return modes
}

// setNet sets a record on a NIC interface, by name.
func (n *Node) setNet(src, name, attr, value string) {
	if key, ok := n.netKeys[name]; ok {
		n.set(src, "net/"+key, attr, value)
	}
}
