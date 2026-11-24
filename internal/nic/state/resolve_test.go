// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package state

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
)

const (
	chartNICs    = "../../../deployments/nvml-mock/helm/nvml-mock/nics"
	rackFixtures = "../testdata/rackprofiles"
)

func loadProfiles(t *testing.T) declaration.Profiles {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(chartNICs, "*.yaml"))
	require.NoError(t, err)
	out := declaration.Profiles{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		p, err := declaration.DecodeProfile(data)
		require.NoError(t, err, path)
		out[p.Name] = p
	}
	return out
}

func loadRack(t *testing.T, name string) *v1alpha1.SGPURackProfile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rackFixtures, name+".yaml"))
	require.NoError(t, err)
	r, err := declaration.DecodeRackProfile(data)
	require.NoError(t, err)
	return r
}

// input is the declaration of one Node of a rack fixture.
func input(t *testing.T, rack string, node Node) Input {
	t.Helper()
	r := loadRack(t, rack)
	return Input{Node: node, Topology: r.Spec.Node.Topology, RDMA: r.Spec.Software.RDMA, Profiles: loadProfiles(t)}
}

func resolve(t *testing.T, in Input) *NICs {
	t.Helper()
	nics, err := Resolve(in)
	require.NoError(t, err)
	return nics
}

func functionAt(t *testing.T, n *NICs, addr string) Function {
	t.Helper()
	i := slices.IndexFunc(n.Functions, func(f Function) bool { return f.Address == addr })
	require.GreaterOrEqual(t, i, 0, "no function at %s", addr)
	return n.Functions[i]
}

func deviceOn(t *testing.T, n *NICs, addr string) RDMADevice {
	t.Helper()
	i := slices.IndexFunc(n.Devices, func(d RDMADevice) bool { return d.Function == addr })
	require.GreaterOrEqual(t, i, 0, "no RDMA device on %s", addr)
	return n.Devices[i]
}

func instance(t *testing.T, n *NICs, slot string) Instance {
	t.Helper()
	i := slices.IndexFunc(n.Instances, func(in Instance) bool { return in.Slot == slot })
	require.GreaterOrEqual(t, i, 0, "no slot %s", slot)
	return n.Instances[i]
}

func TestResolveGB300(t *testing.T) {
	t.Parallel()
	n := resolve(t, input(t, "gb300-nvl72", FileNode("node-a")))

	require.Equal(t, RDMA{UMAD: true, IPoIB: true, UMADMode: "600", DriverVersion: "25.10-2.4.1"}, n.RDMA)
	require.Len(t, n.Instances, 5)

	pf := functionAt(t, n, "0000:03:00.0")
	require.Equal(t, "0x020700", pf.Class)
	require.Equal(t, PCIID{VendorID: "15b3", DeviceID: "1023"}, pf.PCI)
	require.Equal(t, []PCIID{{VendorID: "15b3", DeviceID: "197c"}, {VendorID: "15b3", DeviceID: "197c"}}, pf.Upstream)
	require.Equal(t, "0000:00:00.0", pf.RootPort)
	require.Equal(t, &PCIID{VendorID: "10de", DeviceID: "22b2"}, pf.RootPortID)
	require.Len(t, n.RootPorts, 9)

	// GPUs share the tree: GPU 1 sits below a switch port of nic0, the NIC
	// whose Data Direct function is in its domain.
	require.Equal(t, GPU{Index: 1, Address: "0009:06:00.0", RootComplex: "pci0009:00", NUMANode: 0, Parent: "0009:05:00.0"}, n.GPUs[1])
	require.True(t, slices.ContainsFunc(n.Bridges, func(b Bridge) bool { return b.Address == "0009:05:00.0" && b.Slot == "nic0" }))
	require.Equal(t, &VFType{PCI: PCIID{VendorID: "15b3", DeviceID: "101e"}, Subsystem: PCIID{VendorID: "15b3", DeviceID: "0208"},
		Revision: "0x00", Driver: "mlx5_core"}, pf.VF)
	require.True(t, strings.HasSuffix(pf.VPD["name"], " -Prime"))

	// The Data Direct function sits in GPU 1's domain and shares its ASIC's V3.
	dd := functionAt(t, n, "0009:03:00.0")
	require.Equal(t, "pci0009:00", dd.RootComplex)
	require.Equal(t, pf.VPD["V3"], dd.VPD["V3"])
	require.Equal(t, []int32{1}, instance(t, n, "nic0").GPUs)

	// Both ASICs of board 0 share a serial; their V3 differs.
	nic1 := functionAt(t, n, "0002:03:00.0")
	require.Equal(t, pf.VPD["SN"], nic1.VPD["SN"])
	require.NotEqual(t, pf.VPD["V3"], nic1.VPD["V3"])

	dev := deviceOn(t, n, "0000:03:00.0")
	require.Equal(t, "mlx5_0", dev.Name)
	require.Equal(t, dev.NodeGUID, dev.SystemImageGUID)
	require.Equal(t, map[string]string{"fw_ver": "40.47.3576", "hca_type": "MT4131", "board_id": "MT_0000001513", "hw_rev": "0"},
		dev.Attributes)
	require.Equal(t, []GID{{Index: 0, Address: "fe80:0000:0000:0000:" + dev.NodeGUID, Type: GIDTypeIBRoCEv1}}, dev.Ports[0].GIDs)
	require.Equal(t, int32(4), dev.Ports[0].Planes)

	// systemImageGUID: distinct on the board's second ASIC.
	second := deviceOn(t, n, "0002:03:00.0")
	require.NotEqual(t, second.NodeGUID, second.SystemImageGUID)
	require.Equal(t, "mlx5_1", second.Name)

	// The BlueField-3's two RoCE ports get Ethernet netdevs, and with IPoIB
	// loaded each ConnectX-8 port an IPoIB one, named as the capture shows.
	names := make([]string, 0, len(n.Netdevs))
	for _, nd := range n.Netdevs {
		names = append(names, nd.Name)
	}
	require.Equal(t, []string{"ibp3s0", "ibP2p3s0", "ibP16p3s0", "ibP18p3s0", "enP22p1s0f0np0", "enP22p1s0f1np1"}, names)
	require.Equal(t, "enP22p1s0f0np0", deviceOn(t, n, "0016:01:00.0").Ports[0].Netdev)
	require.Equal(t, instance(t, n, "frontend0").SwitchID, n.Netdevs[5].SwitchID)
}

func TestResolveDeclaredAbsentAndEmptyAttributes(t *testing.T) {
	t.Parallel()
	n := resolve(t, input(t, "aws-c5n", FileNode("node-a")))
	dev := deviceOn(t, n, "0000:00:1f.0")
	require.Equal(t, "efa_0", dev.Name)
	require.Equal(t, "7: unspecified", dev.NodeType)
	require.Equal(t, map[string]string{"fw_ver": ""}, dev.Attributes)
	require.Equal(t, "0.0.0.0", dev.FirmwareVersion, "verbs report the firmware sysfs leaves empty")
	require.Equal(t, "0xEFA0", dev.HWRev)
	require.Empty(t, n.Netdevs)
	require.True(t, strings.HasPrefix(dev.Ports[0].GIDs[0].Address, "fe80:"))
}

func TestResolveSystemImageGUIDFromNamedDevice(t *testing.T) {
	t.Parallel()
	n := resolve(t, input(t, "vr-nvl72", FileNode("node-a")))
	var b0 RDMADevice
	for _, d := range n.Devices {
		if d.Slot == "frontend0" && d.ID == "rdma-b0" {
			b0 = d
		}
	}
	require.NotEmpty(t, b0.NodeGUID)
	for _, d := range n.Devices {
		if d.Slot == "frontend0" {
			require.Equal(t, b0.NodeGUID, d.SystemImageGUID, d.ID)
		}
	}
}

// TestSelectionOrder checks that a slot runs the rack group's override, then
// its own configuration, then its profile's default, and that an unknown
// profile or configuration is rejected.
func TestSelectionOrder(t *testing.T) {
	t.Parallel()
	base := input(t, "gb300-nvl72", FileNode("node-a"))
	configuration := func(in Input, slot string) string {
		return instance(t, resolve(t, in), slot).Configuration
	}

	require.Equal(t, "ib-xdr-4plane", configuration(base, "nic0"), "the profile's default")

	withSlot := base
	withSlot.Topology = base.Topology.DeepCopy()
	withSlot.Topology.NICs[0].Configuration = "eth-multiplane"
	require.Equal(t, "eth-multiplane", configuration(withSlot, "nic0"), "the slot's configuration")

	withOverride := withSlot
	withOverride.Overrides = []v1alpha1.NICOverride{{Slots: []string{"nic0", "nic1"}, Configuration: "ib-xdr-4plane", FirmwareVersion: "40.48.1102"}}
	n := resolve(t, withOverride)
	require.Equal(t, "ib-xdr-4plane", instance(t, n, "nic0").Configuration, "the override wins over the slot")
	require.Equal(t, "40.48.1102", deviceOn(t, n, "0000:03:00.0").FirmwareVersion)
	require.Equal(t, "40.47.3576", deviceOn(t, n, "0010:03:00.0").FirmwareVersion, "a slot the override doesn't name")

	unknown := base
	unknown.Overrides = []v1alpha1.NICOverride{{Slots: []string{"nic0"}, Configuration: "eth-2x400"}}
	_, err := Resolve(unknown)
	require.ErrorContains(t, err, `profile cx8-c8280z has no configuration "eth-2x400"`)

	unknown.Overrides = []v1alpha1.NICOverride{{Slots: []string{"nic0"}, Profile: "cx9-nonexistent"}}
	_, err = Resolve(unknown)
	require.ErrorContains(t, err, `unknown profile "cx9-nonexistent"`)
}

// TestConfigurationSwitch checks that selecting another configuration changes
// a NIC's functions and nothing in its placement: GB300's first ConnectX-8
// goes from one InfiniBand PF to four Ethernet PFs at the same PCI device,
// and its Data Direct function stays where it was.
func TestConfigurationSwitch(t *testing.T) {
	t.Parallel()
	in := input(t, "gb300-nvl72", FileNode("node-a"))
	before := resolve(t, in)
	in.Overrides = []v1alpha1.NICOverride{{Slots: []string{"nic0"}, Configuration: "eth-multiplane"}}
	after := resolve(t, in)

	require.Equal(t, "0x020700", functionAt(t, before, "0000:03:00.0").Class)
	for fn := range 4 {
		addr := fmt.Sprintf("0000:03:00.%d", fn)
		f := functionAt(t, after, addr)
		require.Equal(t, "0x020000", f.Class, addr)
		require.Equal(t, functionAt(t, before, "0000:03:00.0").Upstream, f.Upstream, addr)
		require.Equal(t, functionAt(t, before, "0000:03:00.0").Parent, f.Parent, addr)
		deviceOn(t, after, addr)
	}
	require.Equal(t, functionAt(t, before, "0009:03:00.0"), functionAt(t, after, "0009:03:00.0"), "the Data Direct function doesn't move")
	require.Equal(t, "0x020700", functionAt(t, after, "0002:03:00.0").Class, "the other NICs keep their configuration")
	// Four Ethernet netdevs replace PF0's IPoIB netdev.
	require.Len(t, after.Netdevs, len(before.Netdevs)+3)
	// PF0 sits where it sat, so its RDMA device keeps its GUID, as on hardware.
	require.Equal(t, deviceOn(t, before, "0000:03:00.0").NodeGUID, deviceOn(t, after, "0000:03:00.0").NodeGUID)
}

// identifiers lists every generated identifier on a Node by kind.
func identifiers(n *NICs) map[string][]string {
	out := map[string][]string{}
	for _, d := range n.Devices {
		out["node GUID"] = append(out["node GUID"], d.NodeGUID)
	}
	for _, nd := range n.Netdevs {
		kind := map[string]string{LinkLayerEthernet: "MAC", LinkLayerInfiniBand: "IPoIB address"}[nd.LinkLayer]
		out[kind] = append(out[kind], nd.Address)
	}
	for _, d := range n.Devices {
		for _, p := range d.Ports {
			if p.GUID != "" {
				out["port GUID"] = append(out["port GUID"], p.GUID)
			}
		}
	}
	for _, in := range n.Instances {
		out["switch ID"] = append(out["switch ID"], in.SwitchID)
	}
	// The functions of one ASIC share its V3.
	v3 := map[string]bool{}
	for _, f := range n.Functions {
		if id := f.VPD["V3"]; id != "" && !v3[id] {
			v3[id] = true
			out["V3"] = append(out["V3"], id)
		}
	}
	return out
}

// TestIdentifiersAreDeterministic checks that identifiers depend only on the
// Node's coordinates and where something sits on it.
func TestIdentifiersAreDeterministic(t *testing.T) {
	t.Parallel()
	in := input(t, "gb300-nvl72", RackNode("inventory-uid", "training", 3, 7))
	first, second := resolve(t, in), resolve(t, in)
	require.Equal(t, first, second)

	// Removing nic1 leaves every other NIC's identifiers as they were.
	fewer := in
	fewer.Topology = in.Topology.DeepCopy()
	fewer.Topology.NICs = slices.DeleteFunc(fewer.Topology.NICs, func(s v1alpha1.NICSlot) bool { return s.Slot == "nic1" })
	fewer.Topology.GPUSlots[0].Parent = "" // GPU 0 sat below nic1's switch
	without := resolve(t, fewer)
	for _, d := range without.Devices {
		before := deviceOn(t, first, d.Function)
		require.Equal(t, before.NodeGUID, d.NodeGUID, d.Function)
		require.Equal(t, before.SystemImageGUID, d.SystemImageGUID, d.Function)
	}
	for _, f := range without.Functions {
		require.Equal(t, functionAt(t, first, f.Address).VPD, f.VPD, f.Address)
	}
	for _, n := range without.Netdevs {
		i := slices.IndexFunc(first.Netdevs, func(m Netdev) bool { return m.Name == n.Name })
		require.GreaterOrEqual(t, i, 0, n.Name)
		require.Equal(t, first.Netdevs[i], n)
	}

	// Another Node gets other identifiers, in either mode.
	other := in
	other.Node = RackNode("inventory-uid", "training", 3, 8)
	require.NotEqual(t, identifiers(first), identifiers(resolve(t, other)))
	other.Node = FileNode("node-a")
	require.NotEqual(t, identifiers(first), identifiers(resolve(t, other)))
}

// TestIdentifiersAreUniqueAcrossACluster resolves 4,608 Nodes, 256 racks of
// 18, in each mode and checks that no two NICs share a GUID, a MAC, a switch
// ID or a VPD UUID.
func TestIdentifiersAreUniqueAcrossACluster(t *testing.T) {
	t.Parallel()
	const racks, perRack = 256, 18
	for _, mode := range []string{"control plane", "file"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			in := input(t, "gb300-ethernet", Node{})
			seen := map[string]map[string]string{}
			for rack := range int32(racks) {
				for node := range int32(perRack) {
					in.Node = RackNode("inventory-uid", "training", rack, node)
					if mode == "file" {
						in.Node = FileNode("gb300-" + strconv.Itoa(int(rack*perRack+node)))
					}
					for kind, ids := range identifiers(resolve(t, in)) {
						if seen[kind] == nil {
							seen[kind] = map[string]string{}
						}
						for _, id := range ids {
							prev, dup := seen[kind][id]
							require.False(t, dup, "%s %s on %s and %s", kind, id, prev, in.Node)
							seen[kind][id] = in.Node.String()
						}
					}
				}
			}
			require.Len(t, seen["MAC"], racks*perRack*18)
		})
	}
}

// TestNoVendorOrModelLiterals keeps the resolver generic: everything about a
// NIC model comes from its profile.
func TestNoVendorOrModelLiterals(t *testing.T) {
	t.Parallel()
	forbidden := []string{"mlx", "15b3", "1d0f", "10de", "connectx", "bluefield", "mellanox", "nvidia", "amazon", "aws", "mt4", "efa_", "0x02c9"}
	paths, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(f, func(n ast.Node) bool {
			if _, ok := n.(*ast.ImportSpec); ok {
				return false
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, word := range forbidden {
				require.NotContains(t, strings.ToLower(lit.Value), word, "%s: literal %s", path, lit.Value)
			}
			return true
		})
	}
}

func TestFormatting(t *testing.T) {
	t.Parallel()
	require.Equal(t, "enP1p3s0np0", predictableName("en", "0001:03:00", 0, false, "np0"))
	require.Equal(t, "enP13p65s0f1np1", predictableName("en", "000d:41:00", 1, true, "np1"))
	require.Equal(t, "enp3s0f2np2", predictableName("en", "0000:03:00", 2, true, "np2"))
	require.Equal(t, "ibp3s0", predictableName("ib", "0000:03:00", 0, false, ""))
	require.Equal(t, "ibP16p3s0", predictableName("ib", "0010:03:00", 0, false, ""))
	require.Equal(t, "00:00:00:00:fe:80:00:00:00:00:00:00:01:23:45:67:89:ab:cd:ef", ipoibAddress("0123:4567:89ab:cdef"))
	require.Equal(t, "fe80:0000:0000:0000:0000:00ff:fe00:0001", linkLocalGID("02:00:00:00:00:01"))
	require.Equal(t, PCIID{VendorID: "15b3", DeviceID: "0000"}, subsystem(v1alpha1.NICFunctionType{SubsystemVendorID: "15b3"}))
	require.Equal(t, PCIID{VendorID: "0000", DeviceID: "0208"}, subsystem(v1alpha1.NICFunctionType{SubsystemDeviceID: "0208"}))
	require.Equal(t, "0", sysfsHWRev("0x0"))
	require.Equal(t, "efa0", sysfsHWRev("0xefa0"))
}

// TestIdentifiersIgnoreProfileIDs renames every function, port and RDMA
// device in every NIC profile and checks that no identifier, name or
// placement on any fixture's Node changes: identifiers hash where something
// sits, as a NIC's come from its ASIC, never what a profile calls it.
func TestIdentifiersIgnoreProfileIDs(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(rackFixtures, "*.yaml"))
	require.NoError(t, err)
	for _, path := range paths {
		rack := strings.TrimSuffix(filepath.Base(path), ".yaml")
		t.Run(rack, func(t *testing.T) {
			t.Parallel()
			in := input(t, rack, FileNode("node-a"))
			want := resolve(t, in)

			renamed := input(t, rack, FileNode("node-a"))
			for name, p := range renamed.Profiles {
				renamed.Profiles[name] = renameIDs(p)
			}
			for i, slot := range renamed.Topology.NICs {
				if g := slot.SystemImageGUID; g != "" && g != v1alpha1.SystemImageGUIDDistinct {
					renamed.Topology.NICs[i].SystemImageGUID = "renamed-" + g
				}
			}
			got := resolve(t, renamed)
			require.NotEqual(t, want.Functions[0].ID, got.Functions[0].ID, "the rename must reach the state")
			require.Equal(t, withoutIDs(want), withoutIDs(got))
		})
	}
}

// renameIDs gives every function, port and RDMA device of a profile another
// ID, and every reference to it.
func renameIDs(p *v1alpha1.SNICProfile) *v1alpha1.SNICProfile {
	p = p.DeepCopy()
	rename := func(ids []string) {
		for i := range ids {
			ids[i] = "renamed-" + ids[i]
		}
	}
	for i := range p.Spec.Configurations {
		c := &p.Spec.Configurations[i]
		for j := range c.Ports {
			c.Ports[j].ID = "renamed-" + c.Ports[j].ID
		}
		for j := range c.Functions {
			c.Functions[j].ID = "renamed-" + c.Functions[j].ID
			rename(c.Functions[j].Ports)
		}
		for j := range c.RDMADevices {
			d := &c.RDMADevices[j]
			d.ID, d.Function = "renamed-"+d.ID, "renamed-"+d.Function
			rename(d.Ports)
		}
	}
	return p
}

// withoutIDs is the state with the profile's IDs blanked.
func withoutIDs(n *NICs) *NICs {
	out := *n
	out.Functions = slices.Clone(n.Functions)
	for i := range out.Functions {
		out.Functions[i].ID = ""
	}
	out.Devices = slices.Clone(n.Devices)
	for i := range out.Devices {
		out.Devices[i].ID = ""
		out.Devices[i].Ports = slices.Clone(out.Devices[i].Ports)
		for j := range out.Devices[i].Ports {
			out.Devices[i].Ports[j].ID = ""
		}
	}
	out.Netdevs = slices.Clone(n.Netdevs)
	for i := range out.Netdevs {
		out.Netdevs[i].Port = ""
	}
	return &out
}

// TestIPoIBNetdevs checks the netdev ib_ipoib gives each InfiniBand port: the
// name a captured GB300 tray shows for the ConnectX-8 at 0002:03:00, and an
// address ending in the port's GID.
func TestIPoIBNetdevs(t *testing.T) {
	t.Parallel()
	in := input(t, "gb300-nvl72", FileNode("node-a"))
	n := resolve(t, in)
	d := deviceOn(t, n, "0002:03:00.0")
	i := slices.IndexFunc(n.Netdevs, func(nd Netdev) bool { return nd.Function == "0002:03:00.0" })
	require.GreaterOrEqual(t, i, 0)
	nd := n.Netdevs[i]
	require.Equal(t, "ibP2p3s0", nd.Name)
	require.Equal(t, LinkLayerInfiniBand, nd.LinkLayer)
	require.Equal(t, nd.Name, d.Ports[0].Netdev)
	guid := strings.ReplaceAll(d.NodeGUID, ":", "")
	var bytes []string
	for k := 0; k < len(guid); k += 2 {
		bytes = append(bytes, guid[k:k+2])
	}
	require.Equal(t, "00:00:00:00:fe:80:00:00:00:00:00:00:"+strings.Join(bytes, ":"), nd.Address)
	require.Empty(t, nd.PhysPortName)

	in.RDMA = in.RDMA.DeepCopy()
	in.RDMA.IPoIB = false
	n = resolve(t, in)
	require.False(t, slices.ContainsFunc(n.Netdevs, func(nd Netdev) bool { return nd.LinkLayer == LinkLayerInfiniBand }))
	require.Empty(t, deviceOn(t, n, "0002:03:00.0").Ports[0].Netdev)
}

// TestInfiniBandPortsHaveTheirOwnGUIDs gives an RDMA device a second
// InfiniBand port: the first port's GUID is the node GUID, the second has its
// own, and each has its own GID and IPoIB netdev.
func TestInfiniBandPortsHaveTheirOwnGUIDs(t *testing.T) {
	t.Parallel()
	in := input(t, "gb300-nvl72", FileNode("node-a"))
	p := in.Profiles["cx8-c8280z"].DeepCopy()
	c := &p.Spec.Configurations[0]
	second := c.Ports[0]
	second.ID = "p1"
	c.Ports = append(c.Ports, second)
	c.Functions[0].Ports = append(c.Functions[0].Ports, "p1")
	c.RDMADevices[0].Ports = append(c.RDMADevices[0].Ports, "p1")
	in.Profiles["cx8-c8280z"] = p

	d := deviceOn(t, resolve(t, in), "0002:03:00.0")
	require.Len(t, d.Ports, 2)
	require.Equal(t, d.NodeGUID, d.Ports[0].GUID)
	require.NotEqual(t, d.Ports[0].GUID, d.Ports[1].GUID)
	require.NotEqual(t, d.Ports[0].GIDs[0].Address, d.Ports[1].GIDs[0].Address)
	require.Equal(t, "ibP2p3s0", d.Ports[0].Netdev)
	require.Equal(t, "ibP2p3s0d1", d.Ports[1].Netdev)
}

// TestSlotVPDWithoutProfileVPD keeps a slot's VPD overrides on a profile that
// declares no VPD block.
func TestSlotVPDWithoutProfileVPD(t *testing.T) {
	t.Parallel()
	in := input(t, "gb300-nvl72", FileNode("node-a"))
	p := in.Profiles["cx8-c8280z"].DeepCopy()
	p.Spec.VPD = nil
	in.Profiles["cx8-c8280z"] = p
	in.Topology = in.Topology.DeepCopy()
	i := slices.IndexFunc(in.Topology.NICs, func(s v1alpha1.NICSlot) bool { return s.Slot == "nic1" })
	in.Topology.NICs[i].VPD = map[string]string{"PN": "OEM-0001"}
	require.Equal(t, "OEM-0001", functionAt(t, resolve(t, in), "0002:03:00.0").VPD["PN"])
}

// TestPortOrderDecidesNothing reverses every function's port list, a set,
// and the order of the slots and of each slot's GPUs: the state stays the
// same. A port's identifiers and names follow its physical number, its
// position in the configuration's ports.
func TestPortOrderDecidesNothing(t *testing.T) {
	t.Parallel()
	for _, rack := range []string{"gb300-ethernet", "vr-nvl72", "gb300-nvl72"} {
		in := input(t, rack, FileNode("node-a"))
		want := resolve(t, in)

		shuffled := input(t, rack, FileNode("node-a"))
		for name, p := range shuffled.Profiles {
			p = p.DeepCopy()
			for i := range p.Spec.Configurations {
				c := &p.Spec.Configurations[i]
				for j := range c.Functions {
					slices.Reverse(c.Functions[j].Ports)
				}
			}
			shuffled.Profiles[name] = p
		}
		shuffled.Topology = shuffled.Topology.DeepCopy()
		slices.Reverse(shuffled.Topology.NICs)
		for i := range shuffled.Topology.NICs {
			slices.Reverse(shuffled.Topology.NICs[i].GPUs)
		}
		require.Equal(t, want, resolve(t, shuffled), rack)
	}
}

// TestPortsNumberInDeviceOrder gives an RDMA device two ports listed out of
// physical order: the device numbers them as listed, port 1 taking the node
// GUID, while each port's other identifiers follow its physical number.
func TestPortsNumberInDeviceOrder(t *testing.T) {
	t.Parallel()
	in := input(t, "gb300-nvl72", FileNode("node-a"))
	p := in.Profiles["cx8-c8280z"].DeepCopy()
	c := &p.Spec.Configurations[0]
	second := c.Ports[0]
	second.ID = "p1"
	c.Ports = append(c.Ports, second)
	c.Functions[0].Ports = []string{"p1", "p0"}
	c.RDMADevices[0].Ports = []string{"p1", "p0"}
	in.Profiles["cx8-c8280z"] = p
	d := deviceOn(t, resolve(t, in), "0002:03:00.0")
	require.Equal(t, "p1", d.Ports[0].ID)
	require.Equal(t, 1, d.Ports[0].Number)
	require.Equal(t, d.NodeGUID, d.Ports[0].GUID)
	require.Equal(t, "p0", d.Ports[1].ID)

	// Listing the ports the other way swaps which one is port 1; the second
	// port's GUID follows its physical number either way.
	c.RDMADevices[0].Ports = []string{"p0", "p1"}
	e := deviceOn(t, resolve(t, in), "0002:03:00.0")
	require.Equal(t, "p0", e.Ports[0].ID)
	require.Equal(t, d.NodeGUID, e.Ports[0].GUID)
	require.NotEqual(t, d.Ports[0].GUID, e.Ports[1].GUID, "p1's own GUID differs from the node GUID it took as port 1")
}

// TestBlueFieldVUStartsWithItsSerial checks a VPD identity that reuses
// another's value: on a BlueField-3 the VU is the generated SN followed by
// the function's suffix.
func TestBlueFieldVUStartsWithItsSerial(t *testing.T) {
	t.Parallel()
	n := resolve(t, input(t, "gb300-nvl72", FileNode("node-a")))
	for _, addr := range []string{"0016:01:00.0", "0016:01:00.1"} {
		f := functionAt(t, n, addr)
		require.NotEmpty(t, f.VPD["SN"], addr)
		require.Equal(t, f.VPD["SN"]+"MLNXS0D0F"+addr[len(addr)-1:], f.VPD["VU"], addr)
	}
	require.NotEqual(t, functionAt(t, n, "0016:01:00.0").VPD["SN"], functionAt(t, n, "0000:03:00.0").VPD["SN"])
}

// TestRoCEPortsHaveTwoGIDs checks GIDs 0 and 1 of a RoCE port: the same
// link-local address from the netdev's MAC, as RoCE v1 and RoCE v2, both tied
// to the netdev.
func TestRoCEPortsHaveTwoGIDs(t *testing.T) {
	t.Parallel()
	n := resolve(t, input(t, "gb300-nvl72", FileNode("node-a")))
	port := deviceOn(t, n, "0016:01:00.0").Ports[0]
	i := slices.IndexFunc(n.Netdevs, func(nd Netdev) bool { return nd.Name == port.Netdev })
	require.GreaterOrEqual(t, i, 0)
	ll := linkLocalGID(n.Netdevs[i].Address)
	require.Equal(t, []GID{
		{Index: 0, Address: ll, Type: GIDTypeIBRoCEv1, Netdev: port.Netdev},
		{Index: 1, Address: ll, Type: GIDTypeRoCEv2, Netdev: port.Netdev},
	}, port.GIDs)
}
