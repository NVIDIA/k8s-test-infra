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
	"strings"
)

// pciAttrs are the NIC function sysfs files compared as they read.
var pciAttrs = []string{
	"vendor", "device", "subsystem_vendor", "subsystem_device", "class", "revision", "numa_node", "local_cpulist",
	"current_link_speed", "current_link_width", "max_link_speed", "max_link_width", "sriov_totalvfs", "sriov_numvfs",
	"mlx5_num_vfs", "roce_enable", "modalias", "reset_method", "ari_enabled", "d3cold_allowed", "power_state",
}

const bridgeClass = "0x060400"

// readPCISysfs reads each NIC function, and each bridge of the NIC's own PCIe
// switch, from pci-nic-sysfs.txt.
func (n *Node) readPCISysfs(text string) {
	for _, b := range blocks(text) {
		kv := keyValues(b.lines)
		if kv["class"] == bridgeClass {
			n.set(pciNICSysfs, "bridge/"+b.name, "device", kv["device"])
			n.set(pciNICSysfs, "bridge/"+b.name, "root", rootOf(kv["devpath"]))
			continue
		}
		n.nicFunctions[b.name] = true
		o := "pci/" + b.name
		for _, a := range pciAttrs {
			n.setSysfs(pciNICSysfs, o, a, kv, a)
		}
		for _, a := range []string{"driver", "upstream", "hwmon", "ptp", "aux", "irqs", "files"} {
			n.set(pciNICSysfs, o, a, kv[a])
		}
		n.set(pciNICSysfs, o, "root", rootOf(kv["devpath"]))
		n.set(pciNICSysfs, o, "virtfn_count", kv["virtfns"])
		if pf, ok := kv["physfn"]; ok {
			n.set(pciNICSysfs, o, "physfn", pf)
		}
		// The group's number follows probe order; only having one matters.
		if _, ok := kv["iommu_group"]; ok {
			n.set(pciNICSysfs, o, "iommu_group", "yes")
		}
	}
}

var (
	vpdName    = regexp.MustCompile(`^Product Name: (.*)$`)
	vpdKeyword = regexp.MustCompile(`^\[([0-9A-Z]{2})\] [^:]*: (.*)$`)
)

// readVPD reads each function's vital product data from pci-vpd-fields.txt:
// vpd.name for the product name, vpd.<keyword> for each keyword.
func (n *Node) readVPD(text string) {
	for _, b := range blocks(text) {
		o := "pci/" + b.name
		for _, l := range b.lines {
			if m := vpdName.FindStringSubmatch(l); m != nil {
				n.set(pciVPD, o, "vpd.name", m[1])
			} else if m := vpdKeyword.FindStringSubmatch(l); m != nil {
				n.set(pciVPD, o, "vpd."+m[1], m[2])
			}
		}
	}
}

var (
	lspciHeader = regexp.MustCompile(`^((?:[0-9a-f]{4}:)?[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]) `)
	lspciCap    = regexp.MustCompile(`^Capabilities: \[[^\]]*\] ([^:,]*)`)
	lspciSpeed  = regexp.MustCompile(`Speed ([^,]*)`)
	lspciWidth  = regexp.MustCompile(`Width (x[0-9]+)`)
	dsnValue    = regexp.MustCompile(`^(Device Serial Number) .*`)
)

// readLSPCI reads what lspci decodes from each NIC function's config space:
// its capabilities, its PCIe link and its driver. The vital product data it
// also prints come from pci-vpd-fields.txt.
func (n *Node) readLSPCI(text string) {
	caps := map[string][]string{}
	bdf := ""
	for _, l := range strings.Split(text, "\n") {
		if m := lspciHeader.FindStringSubmatch(l); m != nil {
			bdf = m[1]
			if len(bdf) == 7 { // lspci leaves out domain 0 on a machine with no other
				bdf = "0000:" + bdf
			}
			continue
		}
		if !n.nicFunctions[bdf] {
			continue
		}
		o, t := "pci/"+bdf, strings.TrimSpace(l)
		switch {
		case lspciCap.MatchString(t):
			name := strings.TrimSpace(lspciCap.FindStringSubmatch(t)[1])
			caps[bdf] = append(caps[bdf], dsnValue.ReplaceAllString(name, "$1"))
		case strings.HasPrefix(t, "LnkCap:"), strings.HasPrefix(t, "LnkSta:"):
			n.set(lspciNIC, o, "lspci."+t[:6], lspciField(lspciSpeed, t)+" "+lspciField(lspciWidth, t))
		case strings.HasPrefix(t, "Kernel driver in use:"):
			n.set(lspciNIC, o, "lspci.driver", strings.TrimSpace(strings.TrimPrefix(t, "Kernel driver in use:")))
		}
	}
	for b, c := range caps {
		n.set(lspciNIC, "pci/"+b, "lspci.capabilities", sortedSet(c))
	}
}

func lspciField(re *regexp.Regexp, line string) string {
	if m := re.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// readGPUs reads each GPU's device, NUMA node and root complex from
// pci-all-functions.txt, for pairing GPUs with NICs. The NIC functions it
// lists also count as NIC functions where pci-nic-sysfs.txt was not captured.
func (n *Node) readGPUs(text string) {
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		kv := keyValues(f[1:])
		if kv["vendor"] == "0x15b3" && kv["class"] != bridgeClass {
			n.nicFunctions[f[0]] = true
		}
		if kv["vendor"] != "0x10de" || !strings.HasPrefix(kv["class"], "0x0302") {
			continue
		}
		o := "gpu/" + f[0]
		n.set(pciAll, o, "device", kv["device"])
		n.set(pciAll, o, "numa_node", kv["numa"])
		n.set(pciAll, o, "root", kv["root"])
	}
}
