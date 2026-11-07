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

package profile

import (
	"regexp"
	"strconv"
	"strings"
)

// Parsers for the host and nvidia-smi text files of a capture. Each reads the
// format collect writes and ignores lines it does not recognise.

// keyValues reads key=value lines, such as host-dmi.txt.
func keyValues(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// rdmaDevice is one /sys/class/infiniband device of host-ib-sysfs.txt.
type rdmaDevice struct {
	name      string
	fields    map[string]string
	portState string
	physState string
	rateGbps  int
	linkLayer string
	hwRev     string // from host-ibstat.txt
}

var portLine = regexp.MustCompile(`^port \d+ state=\d+: (\S+) phys=\d+: (\S+) rate=(\d+) Gb/sec.* layer=(\S+)`)

func rdmaDevices(sysfs, ibstat string) []rdmaDevice {
	var out []rdmaDevice
	for _, line := range strings.Split(sysfs, "\n") {
		if name, ok := strings.CutPrefix(line, "== /sys/class/infiniband/"); ok {
			out = append(out, rdmaDevice{name: strings.TrimSpace(name), fields: map[string]string{}})
			continue
		}
		if len(out) == 0 {
			continue
		}
		d := &out[len(out)-1]
		if m := portLine.FindStringSubmatch(line); m != nil {
			d.portState, d.physState, d.linkLayer = m[1], m[2], m[4]
			d.rateGbps, _ = strconv.Atoi(m[3])
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			d.fields[k] = v
		}
	}
	hw := ibstatHardwareVersions(ibstat)
	for i := range out {
		out[i].hwRev = hw[out[i].name]
	}
	return out
}

var caLine = regexp.MustCompile(`^CA '([^']+)'`)

func ibstatHardwareVersions(text string) map[string]string {
	out := map[string]string{}
	ca := ""
	for _, line := range strings.Split(text, "\n") {
		if m := caLine.FindStringSubmatch(line); m != nil {
			ca = m[1]
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Hardware version:"); ok && ca != "" {
			out[ca] = strings.TrimSpace(v)
		}
	}
	return out
}

// pciNUMA reads each function's NUMA node from host-pci-nvidia.txt, keyed by
// lower-case bus ID.
func pciNUMA(text string) map[string]int {
	out := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		path, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		bdf := strings.ToLower(path[strings.LastIndex(path, "/")+1:])
		for _, f := range strings.Fields(rest) {
			if v, ok := strings.CutPrefix(f, "numa="); ok {
				if n, err := strconv.Atoi(v); err == nil {
					out[bdf] = n
				}
			}
		}
	}
	return out
}

// pciRoots reads which root complex (pci<domain>:<bus>) each function of
// host-pci-hierarchy.txt sits under, keyed by lower-case bus ID.
func pciRoots(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "/")
		// /sys/devices/pci0002:80/0002:80:00.0/0002:81:00.0
		if len(parts) < 5 || parts[1] != "sys" || parts[2] != "devices" || !strings.HasPrefix(parts[3], "pci") {
			continue
		}
		out[strings.ToLower(parts[len(parts)-1])] = strings.ToLower(parts[3])
	}
	return out
}

var (
	gpuHeader = regexp.MustCompile(`^GPU (\d+):`)
	linkSpeed = regexp.MustCompile(`^\s*Link (\d+): (.+?)\s*$`)
)

// nvlinkSpeedsByGPU reads `nvidia-smi nvlink -s`: each GPU's per-link speed
// text ("41 GB/s"), in link order.
func nvlinkSpeedsByGPU(text string) map[int][]string {
	out := map[int][]string{}
	gpu := -1
	for _, line := range strings.Split(text, "\n") {
		if m := gpuHeader.FindStringSubmatch(line); m != nil {
			gpu, _ = strconv.Atoi(m[1])
			continue
		}
		if m := linkSpeed.FindStringSubmatch(line); m != nil && gpu >= 0 {
			out[gpu] = append(out[gpu], m[2])
		}
	}
	return out
}

// migName is one GPU instance profile as `nvidia-smi mig -lgip` names it.
type migName struct {
	id   int
	name string
}

var lgipRow = regexp.MustCompile(`^\|\s+(\d+)\s+MIG\s+(\S+)\s+(\d+)\s+`)

// migNames lists GPU 0's instance profiles in the order nvidia-smi prints them.
func migNames(text string) []migName {
	var out []migName
	for _, line := range strings.Split(text, "\n") {
		m := lgipRow.FindStringSubmatch(line)
		if m == nil || m[1] != "0" {
			continue
		}
		id, _ := strconv.Atoi(m[3])
		out = append(out, migName{id: id, name: m[2]})
	}
	return out
}

// migPlacement is one profile's placements from `nvidia-smi mig -lgipp`.
type migPlacement struct {
	starts []int
	size   int
}

var lgippRow = regexp.MustCompile(`^GPU\s+(\d+)\s+Profile ID\s+(\d+)\s+Placements?\s*:\s*\{([\d,]*)\}:(\d+)`)

// migPlacementsByProfile reads GPU 0's placements, keyed by profile ID.
func migPlacementsByProfile(text string) map[int]migPlacement {
	out := map[int]migPlacement{}
	for _, line := range strings.Split(text, "\n") {
		m := lgippRow.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || m[1] != "0" {
			continue
		}
		id, _ := strconv.Atoi(m[2])
		size, _ := strconv.Atoi(m[4])
		var starts []int
		for _, s := range strings.Split(m[3], ",") {
			if n, err := strconv.Atoi(s); err == nil {
				starts = append(starts, n)
			}
		}
		out[id] = migPlacement{starts: starts, size: size}
	}
	return out
}
