// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package capture converts node captures taken before `mokka-capture collect
// network` took NIC surfaces into the listing `mokka-capture nic-surfaces`
// writes, so `mokka-capture diff nic` can compare them. A record the older
// capture lacks is left out, and a capture file it has no counterpart of is
// marked not captured.
package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
)

// notInOlderCaptures are the capture files collect network takes that an
// older capture-node directory has no counterpart of.
var notInOlderCaptures = []string{
	"host-lspci-mellanox-vv.txt", "rdma-system.txt", "rdma-link.txt", "sys-module-rdma.txt",
	"net-ecn-qos.txt", "net-ethtool.txt", "net-ethtool-m.txt", "host-ibstat.txt",
	"devlink-dev-info.txt", "devlink-port.txt", "devlink-eswitch.txt", "devlink-health.txt",
	"fw-mlxconfig.txt", "fw-flint.txt", "cable-mlxlink.txt", "smi-topo-m.txt",
}

// records sets each record with the capture file collect network takes it
// from.
type records struct{ *nicsurfaces.Node }

func (r records) Set(obj, attr, value string) {
	r.Node.Set(obj, attr, value, nicsurfaces.SourceOf(obj, attr))
}

// ConvertNodeCapture reads a node directory written by hack/capture-node.sh,
// such as tests/hardware-captures/vr-nvl72/node-a, and returns its NIC
// surfaces as nic-surfaces would list them.
//
// The older capture has no record of these, so they are left out: PCI link
// speeds, revision, modalias, files, auxiliary devices, IRQs, hwmon and PTP;
// VPD fields other than PN; hw_rev and port lid_mask_count, P_Keys, counters
// and the GID-from-MAC relation; which uverbs file serves which device;
// netdev addr_len, ECN and QoS; module versions; and the RDMA ABI versions.
func ConvertNodeCapture(dir string) (*nicsurfaces.Node, error) {
	c := &converter{dir: dir, r: records{nicsurfaces.New()}, pci: map[string]map[string]string{}, rdmaKeys: map[string]string{}}
	for _, f := range notInOlderCaptures {
		c.r.Missing[f] = "the capture predates collect network"
	}
	for _, step := range []func() error{
		c.pciFunctions, c.pciNICFunctions, c.pciPaths, c.vpd, c.rdmaSysfs, c.netdevs, c.ethtool,
		c.ibvDevinfo, c.devInfiniband, c.node,
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return c.r.Node, nil
}

type converter struct {
	dir      string
	r        records
	pci      map[string]map[string]string // every PCI function: vendor, device, class
	netdev   map[string]string            // netdev name -> object
	rdmaKeys map[string]string            // RDMA device name -> its key, function and role
}

func (c *converter) lines(name string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(c.dir, name))
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}

// fields parses "key=value key=value" after the first word.
func fields(line string) (string, map[string]string) {
	words := strings.Fields(line)
	if len(words) == 0 {
		return "", nil
	}
	kv := map[string]string{}
	for _, w := range words[1:] {
		if k, v, ok := strings.Cut(w, "="); ok {
			kv[k] = v
		}
	}
	return words[0], kv
}

func (c *converter) isNIC(addr string) bool { return c.pci[addr]["vendor"] == "0x15b3" }
func (c *converter) isBridge(addr string) bool {
	return c.isNIC(addr) && c.pci[addr]["class"] == "0x060400"
}

// pciFunctions reads pci-all-functions.txt:
// "0001:03:00.0 vendor=0x15b3 device=0x1025 class=0x020000 numa=0 driver=mlx5_core root=pci0001:00".
func (c *converter) pciFunctions() error {
	lines, err := c.lines("pci-all-functions.txt")
	if err != nil {
		return err
	}
	for _, line := range lines {
		addr, kv := fields(line)
		if addr == "" {
			continue
		}
		c.pci[addr] = kv
		switch {
		case c.isBridge(addr):
			c.r.Set("bridge/"+addr, "device", kv["device"])
			c.r.Set("bridge/"+addr, "root", kv["root"])
		case c.isNIC(addr):
			o := "pci/" + addr
			for _, a := range []string{"vendor", "device", "class", "driver", "root"} {
				c.r.Set(o, a, kv[a])
			}
			c.r.Set(o, "numa_node", kv["numa"])
		case kv["vendor"] == "0x10de" && strings.HasPrefix(kv["class"], "0x0302"):
			c.r.Set("gpu/"+addr, "device", kv["device"])
			c.r.Set("gpu/"+addr, "numa_node", kv["numa"])
			c.r.Set("gpu/"+addr, "root", kv["root"])
		}
	}
	return nil
}

// pciNICFunctions reads pci-nic-functions.txt for subsystem and SR-IOV
// attributes.
func (c *converter) pciNICFunctions() error {
	lines, err := c.lines("pci-nic-functions.txt")
	if err != nil {
		return err
	}
	for _, line := range lines {
		addr, kv := fields(line)
		if !c.isNIC(addr) || c.isBridge(addr) {
			continue
		}
		o := "pci/" + addr
		if v, d, ok := strings.Cut(kv["subsys"], ":"); ok {
			c.r.Set(o, "subsystem_vendor", v)
			c.r.Set(o, "subsystem_device", d)
		}
		c.r.Set(o, "sriov_totalvfs", kv["sriov_totalvfs"])
		c.r.Set(o, "sriov_numvfs", kv["sriov_numvfs"])
		c.r.Set(o, "virtfn_count", kv["virtfn"])
		if kv["physfn"] != "" {
			c.r.Set(o, "physfn", kv["physfn"])
		}
	}
	return nil
}

// pciPaths reads host-pci-hierarchy.txt and records, for each NIC function,
// the IDs of the devices above it, root port first, as nic-surfaces'
// "upstream" does.
func (c *converter) pciPaths() error {
	lines, err := c.lines("host-pci-hierarchy.txt")
	if err != nil {
		return err
	}
	for _, line := range lines {
		parts := strings.Split(strings.TrimPrefix(line, "/sys/devices/"), "/")
		addr := parts[len(parts)-1]
		if !c.isNIC(addr) || c.isBridge(addr) {
			continue
		}
		var ids []string
		for _, up := range parts[1 : len(parts)-1] {
			ids = append(ids, c.pci[up]["vendor"]+":"+c.pci[up]["device"])
		}
		c.r.Set("pci/"+addr, "upstream", strings.Join(ids, ","))
	}
	return nil
}

// vpd reads pci-vpd.txt: "0001:03:00.0: PN 900-9X9BE-00PB-TS5EC".
func (c *converter) vpd() error {
	lines, err := c.lines("pci-vpd.txt")
	if err != nil {
		return err
	}
	for _, line := range lines {
		addr, rest, _ := strings.Cut(line, ": ")
		if pn, ok := strings.CutPrefix(rest, "PN "); ok && c.isNIC(addr) {
			c.r.Set("pci/"+addr, "vpd.PN", strings.TrimSpace(pn))
		}
	}
	return nil
}

var gidLine = regexp.MustCompile(`^gid\[(\d+)\]=(\S+) type=(.+?) ndev=(\S+)$`)

// rdmaSysfs reads rdma-sysfs.txt: "== <dev>" blocks of attributes, then
// "-- port <n>" blocks.
func (c *converter) rdmaSysfs() error {
	lines, err := c.lines("rdma-sysfs.txt")
	if err != nil {
		return err
	}
	var dev *rdmaDevice
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "== "):
			c.setRDMADevice(dev)
			dev = &rdmaDevice{name: strings.TrimPrefix(line, "== "), attrs: map[string]string{}}
		case dev == nil:
		case strings.HasPrefix(line, "-- port "):
			dev.ports = append(dev.ports, &rdmaPort{attrs: map[string]string{}})
		default:
			dev.add(line)
		}
	}
	c.setRDMADevice(dev)
	return nil
}

// rdmaDevice is one "== <dev>" block of rdma-sysfs.txt.
type rdmaDevice struct {
	name  string
	attrs map[string]string
	ports []*rdmaPort
}

type rdmaPort struct {
	attrs       map[string]string
	gids, types []string
	ndevs       int
}

var rdmaPortAttrs = []string{"state", "phys_state", "rate", "link_layer", "lid", "sm_lid", "sm_sl", "cap_mask"}

// add records one line of the device's block: an attribute of the device or
// of its last port, or one of that port's GIDs.
func (d *rdmaDevice) add(line string) {
	if len(d.ports) > 0 && strings.HasPrefix(line, "gid[") {
		d.ports[len(d.ports)-1].addGID(line)
		return
	}
	k, v, ok := strings.Cut(line, "=")
	switch {
	case !ok || strings.HasPrefix(v, "cat:"):
		// absent on this device
	case len(d.ports) == 0:
		d.attrs[k] = v
	case slices.Contains(rdmaPortAttrs, k):
		d.ports[len(d.ports)-1].attrs[k] = v
	}
}

func (p *rdmaPort) addGID(line string) {
	m := gidLine.FindStringSubmatch(line)
	if m == nil || strings.HasPrefix(m[3], "cat:") {
		return // an unused GID table entry
	}
	if strings.Trim(m[2], "0:") != "" {
		p.gids = append(p.gids, m[2])
	}
	p.types = append(p.types, m[3])
	p.ndevs++
}

func (c *converter) setRDMADevice(d *rdmaDevice) {
	if d == nil {
		return
	}
	bdf := d.attrs["devpath"][strings.LastIndex(d.attrs["devpath"], "/")+1:]
	c.rdmaKeys[d.name] = bdf + "/dev"
	o := "rdma/" + bdf + "/dev"
	c.r.Set(o, "name", d.name)
	c.r.Set(o, "pci_function", "yes")
	for _, a := range []string{"node_type", "node_guid", "sys_image_guid", "fw_ver", "hca_type", "board_id", "node_desc"} {
		c.r.Set(o, a, d.attrs[a])
	}
	c.r.Set(o, "ports", strconv.Itoa(len(d.ports)))
	for i, p := range d.ports {
		po := fmt.Sprintf("port/%s/dev/%d", bdf, i+1)
		for k, v := range p.attrs {
			c.r.Set(po, k, v)
		}
		slices.Sort(p.gids)
		slices.Sort(p.types)
		c.r.Set(po, "gids", strings.Join(slices.Compact(p.gids), ","))
		c.r.Set(po, "gid_types", strings.Join(slices.Compact(p.types), ","))
		c.r.Set(po, "gid_ndevs", strconv.Itoa(p.ndevs))
	}
}

var (
	ipLinkName = regexp.MustCompile(`^\d+: ([^:@]+)[:@]`)
	ipLinkPort = regexp.MustCompile(`portname (\S+) parentbus pci parentdev (\S+)`)
)

// netdevs reads net-ip-link.txt for each NIC netdev's function and port name,
// then net-sysfs.txt for its attributes.
func (c *converter) netdevs() error {
	lines, err := c.lines("net-ip-link.txt")
	if err != nil {
		return err
	}
	c.netdev = map[string]string{}
	var current string
	for _, line := range lines {
		if m := ipLinkName.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		if m := ipLinkPort.FindStringSubmatch(line); m != nil && c.isNIC(m[2]) {
			o := "net/" + m[2] + "/" + m[1]
			c.netdev[current] = o
			c.r.Set(o, "name", current)
			c.r.Set(o, "phys_port_name", m[1])
		}
	}
	return c.netdevAttributes()
}

// netdevAttributes reads net-sysfs.txt for the attributes of the netdevs
// netdevs found.
func (c *converter) netdevAttributes() error {
	lines, err := c.lines("net-sysfs.txt")
	if err != nil {
		return err
	}
	var o string
	for _, line := range lines {
		if name, ok := strings.CutPrefix(line, "== "); ok {
			o = c.netdev[name]
			continue
		}
		if o == "" {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "type", "address", "mtu", "speed", "operstate", "carrier", "dev_port", "dev_id":
			c.r.Set(o, k, v)
		}
	}
	return nil
}

// ethtool reads net-ethtool-i.txt: "== <netdev>" blocks of "key: value".
func (c *converter) ethtool() error {
	lines, err := c.lines("net-ethtool-i.txt")
	if err != nil {
		return err
	}
	var o string
	for _, line := range lines {
		if name, ok := strings.CutPrefix(line, "== "); ok {
			o = c.netdev[name]
			continue
		}
		k, v, _ := strings.Cut(line, ": ")
		switch k {
		case "driver", "firmware-version", "bus-info":
			if o != "" {
				c.r.Set(o, "ethtool."+k, strings.TrimSpace(v))
			}
		}
	}
	return nil
}

// ibvDevinfo reads host-ibv-devinfo.txt (ibv_devinfo -v) for the fields
// render rule G10 formats.
func (c *converter) ibvDevinfo() error {
	lines, err := c.lines("host-ibv-devinfo.txt")
	if err != nil {
		return err
	}
	var o, port string
	for _, line := range lines {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "hca_id":
			o, port = c.ibvObject(v), ""
			continue
		case "port":
			port = "port." + v + "."
			continue
		}
		if o == "" {
			continue
		}
		if port == "" {
			switch k {
			case "transport", "fw_ver", "vendor_id", "vendor_part_id", "hw_ver", "board_id", "phys_port_cnt":
				c.r.Set(o, k, v)
			}
			continue
		}
		switch k {
		case "state", "max_mtu", "active_mtu", "port_lmc", "link_layer":
			c.r.Set(o, port+k, v)
		}
	}
	return nil
}

// ibvObject is the object of an RDMA device's ibv_devinfo records, keyed like
// the device, or "" for a device rdma-sysfs.txt doesn't show.
func (c *converter) ibvObject(name string) string {
	if key, ok := c.rdmaKeys[name]; ok {
		return "ibv/" + key
	}
	return ""
}

var lsLine = regexp.MustCompile(`^(c\S+)\s+\d+\s+\S+\s+\S+\s+(\d+),\s*(\d+)\s+.*\s(\S+)$`)

// devInfiniband reads host-ib-dev.txt, an "ls -l /dev/infiniband" listing.
func (c *converter) devInfiniband() error {
	lines, err := c.lines("host-ib-dev.txt")
	if err != nil {
		return err
	}
	var names []string
	for _, line := range lines {
		m := lsLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		names = append(names, m[4])
		if m[4] == "rdma_cm" {
			c.r.Set("devnode/rdma_cm", "devnode", fmt.Sprintf("%s:%s %s", m[2], m[3], octalMode(m[1])))
		}
	}
	slices.Sort(names)
	c.r.Set("node", "dev_infiniband", strings.Join(names, ","))
	return nil
}

// node records the MAD ABI version, which is empty without ib_umad.
func (c *converter) node() error {
	classes, err := c.lines("rdma-classes.txt")
	if err != nil {
		return err
	}
	for i, line := range classes {
		if line == "== /sys/class/infiniband_mad" && i+1 < len(classes) && strings.Contains(classes[i+1], "No such file") {
			c.r.Set("node", "mad_abi_version", "")
		}
	}
	return nil
}

// octalMode turns "crw-rw-rw-" into "666".
func octalMode(s string) string {
	if len(s) < 10 {
		return ""
	}
	var out strings.Builder
	for i := 1; i < 10; i += 3 {
		n := 0
		for j, bit := range []byte{'r', 'w', 'x'} {
			if s[i+j] == bit {
				n |= 4 >> j
			}
		}
		out.WriteString(strconv.Itoa(n))
	}
	return out.String()
}
