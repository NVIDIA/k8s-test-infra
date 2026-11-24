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
	require.Equal(t, "fe80:0000:0000:0000:"+dev.NodeGUID, dev.Ports[0].GID)
	require.Equal(t, int32(4), dev.Ports[0].Planes)

	// systemImageGUID: distinct on the board's second ASIC.
	second := deviceOn(t, n, "0002:03:00.0")
	require.NotEqual(t, second.NodeGUID, second.SystemImageGUID)
	require.Equal(t, "mlx5_1", second.Name)

	// Only RoCE ports get netdevs: the BlueField-3's two.
	require.Len(t, n.Netdevs, 2)
	require.Equal(t, "enP22p1s0f0np0", n.Netdevs[0].Name)
	require.Equal(t, n.Netdevs[0].Name, deviceOn(t, n, "0016:01:00.0").Ports[0].Netdev)
	require.Equal(t, instance(t, n, "frontend0").SwitchID, n.Netdevs[1].SwitchID)
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
	require.True(t, strings.HasPrefix(dev.Ports[0].GID, "fe80:"))
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
	require.Len(t, after.Netdevs, len(before.Netdevs)+4)
}

// identifiers lists every generated identifier on a Node by kind.
func identifiers(n *NICs) map[string][]string {
	out := map[string][]string{}
	for _, d := range n.Devices {
		out["node GUID"] = append(out["node GUID"], d.NodeGUID)
	}
	for _, nd := range n.Netdevs {
		out["MAC"] = append(out["MAC"], nd.MAC)
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
	without := resolve(t, fewer)
	for _, d := range without.Devices {
		before := deviceOn(t, first, d.Function)
		require.Equal(t, before.NodeGUID, d.NodeGUID, d.Function)
		require.Equal(t, before.SystemImageGUID, d.SystemImageGUID, d.Function)
	}
	for _, f := range without.Functions {
		require.Equal(t, functionAt(t, first, f.Address).VPD, f.VPD, f.Address)
	}
	require.Equal(t, first.Netdevs, without.Netdevs)

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
	require.Equal(t, "enP1p3s0np0", predictableName("0001:03:00", 0, false, "p0"))
	require.Equal(t, "enP13p65s0f1np1", predictableName("000d:41:00", 1, true, "p1"))
	require.Equal(t, "enp3s0f2np2", predictableName("0000:03:00", 2, true, "p2"))
	require.Equal(t, "fe80:0000:0000:0000:0000:00ff:fe00:0001", linkLocalGID("02:00:00:00:00:01"))
	require.Equal(t, "0", sysfsHWRev("0x0"))
	require.Equal(t, "efa0", sysfsHWRev("0xefa0"))
}
