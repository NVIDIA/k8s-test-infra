// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package profile

import (
	"path"
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

// colonValues reads "key: value" lines, such as host-lscpu.txt.
func colonValues(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

var (
	numaCPUs = regexp.MustCompile(`^node (\d+) cpus:(.*)$`)
	numaSize = regexp.MustCompile(`^node (\d+) size: (\d+) MB$`)
)

// numaNode is one NUMA node of `numactl -H` and its memory.
type numaNode struct {
	id  string
	mib int64
}

// cpuNodes reads `numactl -H`: each NUMA node that has CPUs, in the order
// listed. A node without CPUs is memory only, such as a GPU's memory on an
// NVLink-C2C board.
func cpuNodes(text string) []numaNode {
	hasCPUs := map[string]bool{}
	var out []numaNode
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if m := numaCPUs.FindStringSubmatch(line); m != nil {
			hasCPUs[m[1]] = strings.TrimSpace(m[2]) != ""
			continue
		}
		if m := numaSize.FindStringSubmatch(line); m != nil && hasCPUs[m[1]] {
			n, _ := strconv.ParseInt(m[2], 10, 64)
			out = append(out, numaNode{id: m[1], mib: n})
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

// rdmaBusIDs reads each RDMA device's PCI function from rdma-sysfs.txt, the
// last element of its devpath, keyed by device name.
func rdmaBusIDs(text string) map[string]string {
	out := map[string]string{}
	name := ""
	for _, line := range strings.Split(text, "\n") {
		if n, ok := strings.CutPrefix(line, "== "); ok {
			name = strings.TrimSpace(n)
			continue
		}
		if p, ok := strings.CutPrefix(line, "devpath="); ok && name != "" {
			out[name] = strings.ToLower(path.Base(strings.TrimSpace(p)))
		}
	}
	return out
}

// virtualFunctions reads which NIC PCI functions of pci-nic-functions.txt are
// SR-IOV virtual functions, keyed by lower-case bus ID: sysfs gives a virtual
// function a physfn link to its parent, and a physical function none.
func virtualFunctions(text string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, f := range fields[1:] {
			if parent, ok := strings.CutPrefix(f, "physfn="); ok {
				out[strings.ToLower(fields[0])] = parent != ""
			}
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
