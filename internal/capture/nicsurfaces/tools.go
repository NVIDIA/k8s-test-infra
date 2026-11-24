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
	"slices"
	"sort"
	"strconv"
	"strings"
)

var devlinkDevice = regexp.MustCompile(`^pci/([^/:\s]+:[^/:\s]+:[^/\s]+?):?$`)

// readDevlink reads devlink dev info (versions and driver, by section, without
// serial numbers), the port flavours, the eswitch mode and the health
// reporters of each NIC function.
func (n *Node) readDevlink(info, ports, eswitch, health string) {
	n.readDevlinkInfo(info)
	n.readDevlinkPorts(ports)
	n.readDevlinkEswitch(eswitch)
	n.readDevlinkHealth(health)
}

// readDevlinkPorts counts each function's devlink ports by flavour.
func (n *Node) readDevlinkPorts(text string) {
	counts := map[string]int{} // bus ID + "\t" + flavour
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		i := slices.Index(f, "flavour")
		if len(f) == 0 || !strings.HasPrefix(f[0], "pci/") || i < 0 || i+1 >= len(f) {
			continue
		}
		bdf, _, _ := strings.Cut(strings.TrimPrefix(f[0], "pci/"), "/")
		counts[bdf+"\t"+f[i+1]]++
	}
	for k, c := range counts {
		bdf, flavour, _ := strings.Cut(k, "\t")
		n.set(devlinkPort, "devlink/"+bdf, "ports."+flavour, strconv.Itoa(c))
	}
}

// readDevlinkEswitch reads each function's eswitch mode line.
func (n *Node) readDevlinkEswitch(text string) {
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || !strings.HasPrefix(f[0], "pci/") {
			continue
		}
		o := "devlink/" + strings.TrimSuffix(strings.TrimPrefix(f[0], "pci/"), ":")
		for i := 1; i+1 < len(f); i += 2 {
			n.set(devlinkESW, o, "eswitch."+f[i], f[i+1])
		}
	}
}

// readDevlinkHealth reads the names of each function's health reporters.
func (n *Node) readDevlinkHealth(text string) {
	reporters := map[string][]string{}
	dev := ""
	for _, l := range strings.Split(text, "\n") {
		if m := devlinkDevice.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			dev = m[1]
			if _, ok := reporters[dev]; !ok {
				reporters[dev] = nil
			}
		} else if f := strings.Fields(l); len(f) >= 2 && f[0] == "reporter" && dev != "" {
			reporters[dev] = append(reporters[dev], f[1])
		}
	}
	for d, r := range reporters {
		n.set(devlinkHealth, "devlink/"+d, "health.reporters", sortedSet(r))
	}
}

func (n *Node) readDevlinkInfo(text string) {
	dev, section := "", ""
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if m := devlinkDevice.FindStringSubmatch(t); m != nil && !strings.HasPrefix(l, " ") {
			dev, section = m[1], ""
			n.set(devlinkInfo, "devlink/"+dev, "present", "yes")
			continue
		}
		switch t {
		case "versions:":
			continue
		case "fixed:", "running:", "stored:":
			section = strings.TrimSuffix(t, ":") + "."
			continue
		}
		f := strings.Fields(t)
		if dev == "" || len(f) != 2 || strings.Contains(f[0], "serial") {
			continue
		}
		n.set(devlinkInfo, "devlink/"+dev, "info."+section+f[0], f[1])
	}
}

var (
	mlxconfigParams = regexp.MustCompile(`^(LINK_TYPE_P[0-9]|NUM_OF_PF|NUM_OF_PLANES_P[0-9]|NUM_OF_VFS|SRIOV_EN|` +
		`LAG_RESOURCE_ALLOCATION|LOAD_BALANCE_MODE_P[0-9]|MULTI_PORT_VHCA_EN|IB_PROTO_WIDTH_EN_MASK_P[0-9])$`)
	moduleSplit = regexp.MustCompile(`^MODULE_SPLIT_M0\[([0-9]+)\]$`)
	flintFields = map[string]bool{"Image type": true, "FW Version": true, "Product Version": true, "PSID": true, "Security Attributes": true}
)

// readFirmware reads, per NIC, the firmware configuration that shapes its
// ports (the current value of each, and the module split) and the firmware
// image flint reports.
func (n *Node) readFirmware(mlxconfig, flint string) {
	for _, b := range blocks(mlxconfig) {
		n.readMLXConfig("fw/"+b.name, b.lines)
	}
	for _, b := range blocks(flint) {
		for _, l := range b.lines {
			if k, v, ok := colonValue(l); ok && flintFields[k] {
				n.set(fwFlint, "fw/"+b.name, "flint."+attrName(k), v)
			}
		}
	}
}

func (n *Node) readMLXConfig(o string, lines []string) {
	split := map[int]string{}
	for _, l := range lines {
		if k, v, ok := colonValue(l); ok && (k == "Description" || k == "Device type") {
			n.set(fwMLXConfig, o, attrName(k), v)
			continue
		}
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "*"))
		switch {
		case len(f) == 2 && moduleSplit.MatchString(f[0]):
			i, _ := strconv.Atoi(moduleSplit.FindStringSubmatch(f[0])[1])
			split[i] = f[1]
		case len(f) >= 3 && mlxconfigParams.MatchString(f[0]):
			n.set(fwMLXConfig, o, f[0], f[2]) // Default, Current, Next Boot
		}
	}
	if len(split) > 0 {
		n.set(fwMLXConfig, o, "MODULE_SPLIT_M0", byIndex(split))
	}
}

// byIndex joins the values in index order.
func byIndex(values map[int]string) string {
	idx := make([]int, 0, len(values))
	for i := range values {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, values[i])
	}
	return strings.Join(out, ",")
}

var cableFields = map[string]bool{
	"Speed": true, "Width": true, "Identifier": true, "Compliance": true, "Cable Technology": true,
	"Cable Type": true, "IB Cable Width": true, "Cable Breakout": true, "Vendor Part Number": true,
}

// readCable reads each port's link and cable as mlxlink -m reports them, and
// how many lanes the cable has.
func (n *Node) readCable(text string) {
	for _, b := range blocks(text) {
		o := "cable/" + b.name
		for _, l := range b.lines {
			k, v, ok := colonValue(l)
			switch {
			case !ok:
			case cableFields[k]:
				n.set(cableMLXLink, o, strings.ReplaceAll(k, " ", "_"), v)
			case k == "Bias Current [mA]":
				n.set(cableMLXLink, o, "lanes", strconv.Itoa(len(strings.Split(v, ","))))
			}
		}
	}
}

var (
	topoLegend = regexp.MustCompile(`^\s*(NIC[0-9]+):\s*(\S+)`)
	ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

// readTopo reads what nvidia-smi topo -m says of each NIC: its relation to
// every GPU.
func (n *Node) readTopo(text string) {
	lines := strings.Split(ansiEscape.ReplaceAllString(text, ""), "\n")
	header := slices.IndexFunc(lines, func(l string) bool {
		cols := strings.Split(l, "\t")
		return len(cols) > 1 && strings.TrimSpace(cols[1]) == "GPU0"
	})
	if header < 0 {
		return
	}
	gpus := topoGPUColumns(strings.Split(lines[header], "\t"))
	names := map[string]string{}
	for _, l := range lines {
		if m := topoLegend.FindStringSubmatch(l); m != nil {
			names[m[1]] = m[2]
		}
	}
	for _, l := range lines[header+1:] {
		cols := strings.Split(l, "\t")
		name, ok := names[strings.TrimSpace(cols[0])]
		if !ok {
			continue
		}
		for i, gpu := range gpus {
			if i < len(cols) {
				n.set(smiTopo, "topo/"+name, gpu, strings.TrimSpace(cols[i]))
			}
		}
	}
}

// topoGPUColumns maps the header's GPU columns to their names. "GPU NUMA ID"
// is not a GPU.
func topoGPUColumns(header []string) map[int]string {
	out := map[int]string{}
	for i, h := range header {
		if h = strings.TrimSpace(h); strings.HasPrefix(h, "GPU") && !strings.Contains(h, " ") {
			out[i] = h
		}
	}
	return out
}
