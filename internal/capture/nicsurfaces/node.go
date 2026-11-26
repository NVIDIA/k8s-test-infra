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

// Package nicsurfaces reads one node's NIC surfaces from a capture node
// directory, as `mokka-capture collect network` writes it, into a normalized
// list of records: object, attribute and value. Objects are keyed by what the
// hardware fixes, the PCI address of a function and a port's number, not by
// names cluster software or probe order chooses, so a renamed device still
// pairs with its counterpart and two nodes' lists compare record by record.
// Every record names the capture file it comes from. It backs
// `mokka-capture nic-surfaces` and `mokka-capture diff nic`.
package nicsurfaces

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The capture files the records come from.
const (
	pciNICSysfs   = "pci-nic-sysfs.txt"
	pciVPD        = "pci-vpd-fields.txt"
	pciAll        = "pci-all-functions.txt"
	lspciNIC      = "host-lspci-mellanox-vv.txt"
	rdmaSysfs     = "rdma-sysfs.txt"
	rdmaChar      = "rdma-char-devices.txt"
	rdmaSystem    = "rdma-system.txt"
	rdmaLink      = "rdma-link.txt"
	sysModules    = "sys-module-rdma.txt"
	netSysfs      = "net-sysfs.txt"
	netECNQoS     = "net-ecn-qos.txt"
	netEthtoolI   = "net-ethtool-i.txt"
	netEthtool    = "net-ethtool.txt"
	netEthtoolM   = "net-ethtool-m.txt"
	ibvDevinfo    = "host-ibv-devinfo.txt"
	ibstat        = "host-ibstat.txt"
	devlinkInfo   = "devlink-dev-info.txt"
	devlinkPort   = "devlink-port.txt"
	devlinkESW    = "devlink-eswitch.txt"
	devlinkHealth = "devlink-health.txt"
	fwMLXConfig   = "fw-mlxconfig.txt"
	fwFlint       = "fw-flint.txt"
	cableMLXLink  = "cable-mlxlink.txt"
	smiTopo       = "smi-topo-m.txt"
)

// Sources lists every capture file the records can come from.
var Sources = []string{
	pciNICSysfs, pciVPD, pciAll, lspciNIC, rdmaSysfs, rdmaChar, rdmaSystem, rdmaLink, sysModules,
	netSysfs, netECNQoS, netEthtoolI, netEthtool, netEthtoolM, ibvDevinfo, ibstat,
	devlinkInfo, devlinkPort, devlinkESW, devlinkHealth, fwMLXConfig, fwFlint, cableMLXLink, smiTopo,
}

// Node is one node's NIC surfaces.
type Node struct {
	// Records maps object to attribute to value.
	Records map[string]map[string]string
	// Missing holds the sources the capture lacks, and why: absent from the
	// directory, or marked FAILED in its errors.log because the capturing pod
	// lacked the tool or the connection dropped. Their records are a capture
	// blind spot, not a difference. A tool that ran and failed, as one does
	// where the surface it reads does not exist, counts as captured.
	Missing map[string]string

	// source lists, per object + "\t" + attribute, the capture files a record
	// was read or derived from, the one it was read from first.
	source map[string][]string
	first  map[string]string // object -> the source that first gave it a record

	// Filled while reading, for keying and relating records across sources.
	nicFunctions map[string]bool   // NIC PCI functions, by bus ID
	rdmaKeys     map[string]string // RDMA device name -> key (bus ID and role)
	netKeys      map[string]string // NIC interface name -> key
	netFunction  map[string]string // NIC interface name -> bus ID
	macs         map[string]string // every interface name -> address
}

func newNode() *Node {
	return &Node{
		Records: map[string]map[string]string{}, Missing: map[string]string{},
		source: map[string][]string{}, first: map[string]string{},
		nicFunctions: map[string]bool{}, rdmaKeys: map[string]string{},
		netKeys: map[string]string{}, netFunction: map[string]string{}, macs: map[string]string{},
	}
}

// Read reads the NIC surfaces from a capture node directory, or from a
// listing `mokka-capture nic-surfaces` wrote, given as a file.
func Read(dir string) (*Node, error) {
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return readListingFile(dir)
	}
	failed, err := failedFiles(dir)
	if err != nil {
		return nil, err
	}
	n := newNode()
	text := map[string]string{}
	for _, f := range Sources {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			n.Missing[f] = "no such file"
		case err != nil:
			return nil, err
		case failed[f] != "":
			n.Missing[f] = failed[f]
		default:
			text[f] = string(raw)
		}
	}
	if len(text) == 0 {
		return nil, fmt.Errorf("%s: no NIC surface files; capture it with mokka-capture collect network", dir)
	}
	n.read(text)
	return n, nil
}

// read parses the sources in an order where each finds the keys it needs:
// PCI functions first, then the interfaces and RDMA devices on them, then
// what tools print per device name.
func (n *Node) read(text map[string]string) {
	n.readPCISysfs(text[pciNICSysfs])
	n.readVPD(text[pciVPD])
	n.readLSPCI(text[lspciNIC])
	n.readGPUs(text[pciAll])
	n.readNetSysfs(text[netSysfs])
	n.readRDMASysfs(text[rdmaSysfs])
	n.relateIPoIB()
	n.readCharDevices(text[rdmaChar])
	n.readModules(text[sysModules])
	n.readRDMATool(text[rdmaSystem], text[rdmaLink])
	n.readECNQoS(text[netECNQoS])
	n.readEthtool(text[netEthtoolI], text[netEthtool], text[netEthtoolM])
	n.readIBVDevinfo(text[ibvDevinfo])
	n.readIBStat(text[ibstat])
	n.readDevlink(text[devlinkInfo], text[devlinkPort], text[devlinkESW], text[devlinkHealth])
	n.readFirmware(text[fwMLXConfig], text[fwFlint])
	n.readCable(text[cableMLXLink])
	n.readTopo(text[smiTopo])
	n.keyByDevice()
}

var (
	toolMissing    = regexp.MustCompile(`executable file not found|: not found|command not found|none of .* found|\bnot found$|exit code 127$`)
	connectionLost = regexp.MustCompile(`Unable to connect to the server|error dialing backend|TLS handshake timeout|connection refused|connection reset by peer|i/o timeout`)
)

// failedFiles reads errors.log, where each try of a command that failed or
// printed an error starts with a "--- <file>, try <n>" heading and a failure
// ends with "FAILED: <file>", and says why each failed file was not captured:
// the capturing pod lacked the tool, or the connection to the cluster dropped.
// Only the last try's lines count. A file whose tool ran and failed maps to
// "": what it printed is the capture.
func failedFiles(dir string) (map[string]string, error) {
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(dir, "errors.log"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var stderr []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "--- ") {
			stderr = nil
			continue
		}
		f, ok := strings.CutPrefix(strings.TrimSpace(l), "FAILED: ")
		if !ok {
			stderr = append(stderr, l)
			continue
		}
		out[strings.Fields(f + " ")[0]] = failureReason(stderr)
		stderr = nil
	}
	return out, nil
}

// failureReason reads a failed command's standard error from the end, so a
// command that lost its connection, was run again and then failed is judged
// by its last try.
func failureReason(stderr []string) string {
	for i := len(stderr) - 1; i >= 0; i-- {
		switch l := stderr[i]; {
		case toolMissing.MatchString(l):
			return "the capturing pod lacks the tool"
		case connectionLost.MatchString(l):
			return "the connection to the cluster dropped"
		}
	}
	return ""
}

func (n *Node) set(src, obj, attr, value string) { n.setFrom([]string{src}, obj, attr, value) }

// setFrom sets a record derived from several capture files, such as a
// relation between a GID and an interface's MAC: without any one of them the
// record can't be derived, so its absence is a blind spot, not a difference.
func (n *Node) setFrom(srcs []string, obj, attr, value string) {
	if n.Records[obj] == nil {
		n.Records[obj] = map[string]string{}
		n.first[obj] = srcs[0]
	}
	n.Records[obj][attr] = value
	n.source[obj+"\t"+attr] = srcs
}

func (n *Node) get(obj, attr string) (string, bool) {
	v, ok := n.Records[obj][attr]
	return v, ok
}

// Source names the capture file a record comes from, or with attr "" the file
// that first gave the object a record.
func (n *Node) Source(obj, attr string) string {
	if attr == "" {
		return n.first[obj]
	}
	if srcs := n.source[obj+"\t"+attr]; len(srcs) > 0 {
		return srcs[0]
	}
	return ""
}

// Sources names every capture file a record was read or derived from.
func (n *Node) Sources(obj, attr string) []string { return n.source[obj+"\t"+attr] }

// Set adds a record that no capture file holds, such as a relation between
// records, with the files it was derived from.
func (n *Node) Set(obj, attr, value string, sources ...string) { n.setFrom(sources, obj, attr, value) }

// Get gives diff nic's relations the records to relate.
func (n *Node) Get(obj, attr string) (string, bool) { return n.get(obj, attr) }

// BindUnbound gives the RDMA devices of n that are no PCI function the key of
// other's device of the same name and role, along with their ports, device
// nodes and tool records, so their values compare. Their pci_function record
// still shows the difference.
func (n *Node) BindUnbound(other *Node) {
	keyOf := map[string]string{} // "name/role" -> "bus ID/role" in other
	for _, o := range other.Objects("rdma") {
		key := strings.TrimPrefix(o, "rdma/")
		if strings.HasPrefix(key, "unbound/") {
			continue
		}
		if name, ok := other.get(o, "name"); ok {
			keyOf[name+"/"+key[strings.LastIndex(key, "/")+1:]] = key
		}
	}
	objs := make([]string, 0, len(n.Records))
	for o := range n.Records {
		objs = append(objs, o)
	}
	sort.Strings(objs)
	for _, o := range objs {
		kind, rest, _ := strings.Cut(o, "/")
		parts := strings.SplitN(strings.TrimPrefix(rest, "unbound/"), "/", 3) // name, role, the rest
		if !strings.HasPrefix(rest, "unbound/") || len(parts) < 2 {
			continue
		}
		key, ok := keyOf[parts[0]+"/"+parts[1]]
		if !ok {
			continue
		}
		renamed := kind + "/" + key
		if len(parts) == 3 {
			renamed += "/" + parts[2]
		}
		n.rename(o, renamed)
	}
}

// rename keeps each record's sources, so a record renamed to its device's key
// still names the files it came from.
func (n *Node) rename(from, to string) {
	for a, v := range n.Records[from] {
		n.setFrom(n.source[from+"\t"+a], to, a, v)
		delete(n.source, from+"\t"+a)
	}
	delete(n.Records, from)
	delete(n.first, from)
}

// Objects returns the objects of a kind, such as "rdma", sorted.
func (n *Node) Objects(kind string) []string {
	var out []string
	for o := range n.Records {
		if Kind(o) == kind {
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}

// Kind is the kind of an object: what comes before its first slash.
func Kind(obj string) string {
	k, _, _ := strings.Cut(obj, "/")
	return k
}

// Write prints the records, one per line, as object, attribute, value and
// source files (joined by + where a record was derived from several)
// separated by tabs, after a comment naming the sources not captured.
func (n *Node) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	var missing []string
	for _, f := range Sources {
		if why, ok := n.Missing[f]; ok {
			missing = append(missing, f+" ("+why+")")
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(bw, "# not captured: %s\n", strings.Join(missing, ", "))
	}
	objs := make([]string, 0, len(n.Records))
	for o := range n.Records {
		objs = append(objs, o)
	}
	sort.Strings(objs)
	for _, o := range objs {
		attrs := make([]string, 0, len(n.Records[o]))
		for a := range n.Records[o] {
			attrs = append(attrs, a)
		}
		sort.Strings(attrs)
		for _, a := range attrs {
			_, _ = fmt.Fprintf(bw, "%s\t%s\t%s\t%s\n", o, a, oneLine(n.Records[o][a]), strings.Join(n.Sources(o, a), "+"))
		}
	}
	return bw.Flush()
}

func oneLine(v string) string {
	return strings.NewReplacer("\n", "; ", "\t", " ").Replace(v)
}

// OneLine is a value as a listing prints it, with tabs as spaces and line
// breaks as "; ", so a value read from a capture compares with the same value
// read from a listing.
func OneLine(v string) string { return oneLine(v) }
