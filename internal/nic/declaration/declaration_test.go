// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

const (
	chartNICs    = "../../../deployments/nvml-mock/helm/nvml-mock/nics"
	rackFixtures = "../testdata/rackprofiles"
)

func ptr[T any](v T) *T { return &v }

func validProfile() *v1alpha1.SNICProfile {
	p := &v1alpha1.SNICProfile{Spec: v1alpha1.SNICProfileSpec{
		Identity: v1alpha1.NICIdentity{Model: "nic"},
		FunctionTypes: map[string]v1alpha1.NICFunctionType{
			"network": {VendorID: "15b3", DeviceID: "1025", Driver: "mlx5_core"},
		},
		DefaultConfiguration: "eth",
		Configurations: []v1alpha1.NICConfiguration{{
			Name:        "eth",
			Ports:       []v1alpha1.NICPort{{ID: "p0", Technology: v1alpha1.NICTechnologyRoCE, Width: 1, LaneSpeed: "XDR"}},
			Functions:   []v1alpha1.NICFunction{{ID: "pf0", Type: "network", PCIDevice: "network", Class: "0x020000", Ports: []string{"p0"}}},
			RDMADevices: []v1alpha1.NICRDMADevice{{ID: "r0", Function: "pf0", Ports: []string{"p0"}}},
		}},
	}}
	p.Name = "nic"
	return p
}

func TestProfileValidate(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateProfile(validProfile()))

	for _, tc := range []struct {
		name   string
		mutate func(*v1alpha1.SNICProfile)
		want   string
	}{
		{"unknown technology", func(p *v1alpha1.SNICProfile) { p.Spec.Configurations[0].Ports[0].Technology = "ethernet" }, "unknown technology"},
		{"unknown lane speed", func(p *v1alpha1.SNICProfile) { p.Spec.Configurations[0].Ports[0].LaneSpeed = "XXX" }, "unknown laneSpeed"},
		{"missing default", func(p *v1alpha1.SNICProfile) { p.Spec.DefaultConfiguration = "ib" }, "defaultConfiguration"},
		{"duplicate configuration", func(p *v1alpha1.SNICProfile) {
			p.Spec.Configurations = append(p.Spec.Configurations, p.Spec.Configurations[0])
		}, `configuration name "eth" is empty or used twice`},
		{"port owned twice", func(p *v1alpha1.SNICProfile) {
			c := &p.Spec.Configurations[0]
			c.Functions = append(c.Functions, v1alpha1.NICFunction{ID: "pf1", Type: "network", PCIDevice: "network", Function: 1, Class: "0x020000", Ports: []string{"p0"}})
		}, "owned by pf0 and pf1"},
		{"unowned port", func(p *v1alpha1.SNICProfile) {
			c := &p.Spec.Configurations[0]
			c.Ports = append(c.Ports, v1alpha1.NICPort{ID: "p1", Technology: v1alpha1.NICTechnologyRoCE, Width: 1, LaneSpeed: "XDR"})
		}, "port p1 has no function"},
		{"function number used twice", func(p *v1alpha1.SNICProfile) {
			c := &p.Spec.Configurations[0]
			c.Functions = append(c.Functions, v1alpha1.NICFunction{ID: "pf1", Type: "network", PCIDevice: "network", Class: "0x020000"})
		}, "network.0 is used twice"},
		{"bad class", func(p *v1alpha1.SNICProfile) { p.Spec.Configurations[0].Functions[0].Class = "0200" }, "class"},
		{"unknown function type", func(p *v1alpha1.SNICProfile) { p.Spec.Configurations[0].Functions[0].Type = "vf" }, `unknown type "vf"`},
		{"rdma on unknown function", func(p *v1alpha1.SNICProfile) { p.Spec.Configurations[0].RDMADevices[0].Function = "pf9" }, "unknown function"},
		{"rdma without ports", func(p *v1alpha1.SNICProfile) { p.Spec.Configurations[0].RDMADevices[0].Ports = nil }, "has no ports"},
		{"function type without a driver", func(p *v1alpha1.SNICProfile) {
			ft := p.Spec.FunctionTypes["network"]
			ft.Driver = ""
			p.Spec.FunctionTypes["network"] = ft
		}, "driver is empty"},
		{"uppercase PCI ID", func(p *v1alpha1.SNICProfile) {
			ft := p.Spec.FunctionTypes["network"]
			ft.DeviceID = "A2DC"
			p.Spec.FunctionTypes["network"] = ft
		}, "not four lowercase hex digits"},
		{"attribute both absent and empty", func(p *v1alpha1.SNICProfile) {
			ft := p.Spec.FunctionTypes["network"]
			ft.RDMAOmit, ft.RDMAEmpty = []v1alpha1.RDMAAttribute{"fw_ver"}, []v1alpha1.RDMAAttribute{"fw_ver"}
			p.Spec.FunctionTypes["network"] = ft
		}, "fw_ver is in both rdmaOmit and rdmaEmpty"},
		{"unknown optional attribute", func(p *v1alpha1.SNICProfile) {
			ft := p.Spec.FunctionTypes["network"]
			ft.RDMAOmit = []v1alpha1.RDMAAttribute{"node_guid"}
			p.Spec.FunctionTypes["network"] = ft
		}, `"node_guid" can't be omitted`},
		{"VF type that doesn't exist", func(p *v1alpha1.SNICProfile) {
			ft := p.Spec.FunctionTypes["network"]
			ft.TotalVFs, ft.VFType = 16, "vf"
			p.Spec.FunctionTypes["network"] = ft
		}, `vfType "vf" is not another function type`},
		{"VF type without SR-IOV", func(p *v1alpha1.SNICProfile) {
			ft := p.Spec.FunctionTypes["network"]
			ft.VFType = "vf"
			p.Spec.FunctionTypes["network"] = ft
			p.Spec.FunctionTypes["vf"] = v1alpha1.NICFunctionType{VendorID: "15b3", DeviceID: "101e", Driver: "mlx5_core"}
		}, "vfType needs totalVFs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := validProfile()
			tc.mutate(p)
			err := ValidateProfile(p)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestVFType(t *testing.T) {
	t.Parallel()
	p := validProfile()
	ft := p.Spec.FunctionTypes["network"]
	ft.TotalVFs, ft.VFType = 16, "vf"
	p.Spec.FunctionTypes["network"] = ft
	p.Spec.FunctionTypes["vf"] = v1alpha1.NICFunctionType{VendorID: "15b3", DeviceID: "101e", Driver: "mlx5_core"}
	require.NoError(t, ValidateProfile(p))
}

func singleSlot() *v1alpha1.SGPUTopology {
	return &v1alpha1.SGPUTopology{NICs: []v1alpha1.NICSlot{{
		Slot: "nic0", Profile: "nic",
		Devices: map[string]v1alpha1.NICDevicePlacement{"network": {
			RootComplex: "pci0003:00", RootPort: "0003:00:00.0", Address: "0003:01:00", Parent: "0003:00:00.0",
		}},
	}}}
}

func setDevice(topo *v1alpha1.SGPUTopology, slot int, name string, mutate func(*v1alpha1.NICDevicePlacement)) {
	d := topo.NICs[slot].Devices[name]
	mutate(&d)
	topo.NICs[slot].Devices[name] = d
}

func TestNodeValidate(t *testing.T) {
	t.Parallel()
	profiles := Profiles{"nic": validProfile()}
	require.NoError(t, ValidateNode(singleSlot(), nil, profiles))

	for _, tc := range []struct {
		name   string
		mutate func(*v1alpha1.SGPUTopology)
		want   string
	}{
		{"unknown profile", func(r *v1alpha1.SGPUTopology) { r.NICs[0].Profile = "x" }, "unknown profile"},
		{"unknown configuration", func(r *v1alpha1.SGPUTopology) { r.NICs[0].Configuration = "ib" }, `no configuration "ib"`},
		{"duplicate slot", func(r *v1alpha1.SGPUTopology) {
			s := *r.NICs[0].DeepCopy()
			s.Devices = map[string]v1alpha1.NICDevicePlacement{"network": {
				RootComplex: "pci0004:00", RootPort: "0004:00:00.0", Address: "0004:01:00", Parent: "0004:00:00.0",
			}}
			r.NICs = append(r.NICs, s)
		}, `slot name "nic0" is empty or used twice`},
		{"device not placed", func(r *v1alpha1.SGPUTopology) {
			r.NICs[0].Devices = map[string]v1alpha1.NICDevicePlacement{}
		}, "doesn't place"},
		{"bad parent", func(r *v1alpha1.SGPUTopology) {
			setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) { d.Parent = "0003:09:00.0" })
		}, "not the root port or a bridge"},
		{"bridge without switch port", func(r *v1alpha1.SGPUTopology) {
			setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) {
				d.Bridges = []v1alpha1.PCIBridge{{Address: "0003:01:00.0", Parent: "0003:00:00.0"}}
			})
		}, "no switchPort"},
		{"bridge before its parent", func(r *v1alpha1.SGPUTopology) {
			setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) {
				d.Bridges = []v1alpha1.PCIBridge{
					{Address: "0003:02:00.0", Parent: "0003:01:00.0"},
					{Address: "0003:01:00.0", Parent: "0003:00:00.0"},
				}
			})
		}, "not the root port or an earlier bridge"},
		{"address used twice", func(r *v1alpha1.SGPUTopology) {
			s := *r.NICs[0].DeepCopy()
			s.Slot = "nic1"
			r.NICs = append(r.NICs, s)
		}, "used by slot nic0 and slot nic1"},
		{"NIC on a GPU's address", func(r *v1alpha1.SGPUTopology) {
			r.GPUSlots = []v1alpha1.GPUSlot{{Index: 0, RootComplex: "pci0003:00", PCIAddress: "0003:01:00.0"}}
		}, "used by GPU 0 and slot nic0"},
		{"device outside its root complex", func(r *v1alpha1.SGPUTopology) {
			setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) { d.Address = "0004:01:00" })
		}, "in pci0003:00"},
		{"device on the root bus with a parent", func(r *v1alpha1.SGPUTopology) {
			setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) { d.RootPort = "" })
		}, "a device on the root bus has no parent"},
		{"NUMA node below unknown", func(r *v1alpha1.SGPUTopology) {
			setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) { d.NUMANode = -2 })
		}, "numaNode -2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			topo := singleSlot()
			tc.mutate(topo)
			require.ErrorContains(t, ValidateNode(topo, nil, profiles), tc.want)
		})
	}
}

func TestVPDAndPlacementValidate(t *testing.T) {
	t.Parallel()
	withVPD := func() (Profiles, *v1alpha1.SGPUTopology) {
		p := validProfile()
		p.Spec.VPD = &v1alpha1.NICVPD{Identities: map[string]v1alpha1.VPDScope{
			"SN": v1alpha1.VPDScopeBoard, "VU": v1alpha1.VPDScopeBoardPosition,
		}}
		topo := &v1alpha1.SGPUTopology{
			GPUSlots: []v1alpha1.GPUSlot{{Index: 0, RootComplex: "pci0008:00", PCIAddress: "0008:06:00.0"}},
			NICs: []v1alpha1.NICSlot{{
				Slot: "nic0", Profile: "nic", Board: ptr[int32](0), GPUs: []int32{0},
				Devices: map[string]v1alpha1.NICDevicePlacement{"network": {
					RootComplex: "pci0003:00", RootPort: "0003:00:00.0", Address: "0003:01:00",
					Parent: "0003:00:00.0", VPDPosition: "-Prime",
				}},
			}},
		}
		return Profiles{"nic": p}, topo
	}
	profiles, topo := withVPD()
	require.NoError(t, ValidateProfile(profiles["nic"]))
	require.NoError(t, ValidateNode(topo, nil, profiles))

	for _, tc := range []struct {
		name   string
		mutate func(Profiles, *v1alpha1.SGPUTopology)
		want   string
	}{
		{"unknown scope", func(p Profiles, _ *v1alpha1.SGPUTopology) {
			p["nic"].Spec.VPD.Identities["V3"] = "chip"
		}, "unknown scope"},
		{"keyword both model and identity", func(p Profiles, _ *v1alpha1.SGPUTopology) {
			p["nic"].Spec.VPD.Keywords = map[string]string{"SN": "x"}
		}, "both a model value and an identity"},
		{"board scope without a board", func(_ Profiles, r *v1alpha1.SGPUTopology) {
			r.NICs[0].Board = nil
		}, "names no board"},
		{"board position without a position", func(_ Profiles, r *v1alpha1.SGPUTopology) {
			setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) { d.VPDPosition = "" })
		}, "has no vpdPosition"},
		{"bad system image GUID mode", func(_ Profiles, r *v1alpha1.SGPUTopology) {
			r.NICs[0].SystemImageGUID = "shared"
		}, "systemImageGUID"},
		{"GPU without a slot", func(_ Profiles, r *v1alpha1.SGPUTopology) {
			r.NICs[0].GPUs = []int32{3}
		}, "GPU 3 has no GPU slot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, r := withVPD()
			tc.mutate(p, r)
			err := errors.Join(ValidateProfile(p["nic"]), ValidateNode(r, nil, p))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// TestMultiHostPorts checks that one port may belong to one function per PCI
// device, as on a NIC with several host links, but not to two functions of
// one device.
func TestMultiHostPorts(t *testing.T) {
	t.Parallel()
	p := validProfile()
	c := &p.Spec.Configurations[0]
	c.Functions = append(c.Functions, v1alpha1.NICFunction{ID: "b0", Type: "network", PCIDevice: "hostB", Class: "0x020000", Ports: []string{"p0"}})
	require.NoError(t, ValidateProfile(p))

	c.Functions = append(c.Functions, v1alpha1.NICFunction{ID: "b1", Type: "network", PCIDevice: "hostB", Function: 1, Class: "0x020000", Ports: []string{"p0"}})
	require.ErrorContains(t, ValidateProfile(p), "owned by b0 and b1")
}

// TestBoardHoldsOneModel checks that board numbers are Node-wide: two slots
// on one board must use the same NIC profile.
func TestBoardHoldsOneModel(t *testing.T) {
	t.Parallel()
	other := validProfile()
	other.Name = "other"
	profiles := Profiles{"nic": validProfile(), "other": other}
	slot := func(name, profile, domain string) v1alpha1.NICSlot {
		return v1alpha1.NICSlot{Slot: name, Profile: profile, Board: ptr[int32](0), Devices: map[string]v1alpha1.NICDevicePlacement{"network": {
			RootComplex: "pci" + domain + ":00", RootPort: domain + ":00:00.0", Address: domain + ":01:00", Parent: domain + ":00:00.0",
		}}}
	}
	topo := &v1alpha1.SGPUTopology{NICs: []v1alpha1.NICSlot{slot("a", "nic", "0003"), slot("b", "nic", "0004")}}
	require.NoError(t, ValidateNode(topo, nil, profiles))
	topo.NICs[1].Profile = "other"
	require.ErrorContains(t, ValidateNode(topo, nil, profiles), "board 0 holds both nic and other")
}

func TestSelect(t *testing.T) {
	t.Parallel()
	slots := []v1alpha1.NICSlot{
		{Slot: "nic0", Profile: "a", Configuration: "ib", FirmwareVersion: "1", VPD: map[string]string{"EC": "A1"}},
		{Slot: "nic1", Profile: "a", Configuration: "ib"},
	}

	got, err := Select(slots, []v1alpha1.NICOverride{
		{Slots: []string{"nic0", "nic1"}, Configuration: "eth", VPD: map[string]string{"EC": "A3"}},
		{Slots: []string{"nic1"}, FirmwareVersion: "2"},
	})
	require.NoError(t, err)
	require.Equal(t, "eth", got[0].Configuration)
	require.Equal(t, "1", got[0].FirmwareVersion, "a field no override sets keeps the slot's value")
	require.Equal(t, map[string]string{"EC": "A3"}, got[0].VPD)
	require.Equal(t, "2", got[1].FirmwareVersion)
	require.Equal(t, "A1", slots[0].VPD["EC"], "the declared slots are left unchanged")

	// A new profile drops the slot's configuration, so its default applies;
	// a later override can still pick one of the new profile's.
	got, err = Select(slots, []v1alpha1.NICOverride{{Slots: []string{"nic0"}, Profile: "b"}})
	require.NoError(t, err)
	require.Equal(t, v1alpha1.NICSlot{Slot: "nic0", Profile: "b", FirmwareVersion: "1", VPD: map[string]string{"EC": "A1"}}, got[0])
	got, err = Select(slots, []v1alpha1.NICOverride{
		{Slots: []string{"nic0"}, Profile: "b"},
		{Slots: []string{"nic0"}, Configuration: "roce"},
	})
	require.NoError(t, err)
	require.Equal(t, "roce", got[0].Configuration)

	_, err = Select(slots, []v1alpha1.NICOverride{{Slots: []string{"nic9"}, Configuration: "eth"}})
	require.ErrorContains(t, err, `unknown slot "nic9"`)
	_, err = Select(slots, []v1alpha1.NICOverride{{Configuration: "eth"}})
	require.ErrorContains(t, err, "names no slots")
}

// TestOverridesAreValidated checks that the rack rules run on the slots as the
// overrides leave them: an override may select a profile or configuration
// whose PCI devices the slot doesn't place.
func TestOverridesAreValidated(t *testing.T) {
	t.Parallel()
	twoHost := validProfile()
	twoHost.Name = "two-host"
	c := &twoHost.Spec.Configurations[0]
	c.Functions = append(c.Functions, v1alpha1.NICFunction{ID: "b0", Type: "network", PCIDevice: "hostB", Class: "0x020000", Ports: []string{"p0"}})
	twoHost.Spec.Configurations = append(twoHost.Spec.Configurations, v1alpha1.NICConfiguration{
		Name:      "one-host",
		Ports:     c.Ports,
		Functions: c.Functions[:1],
	})
	profiles := Profiles{"nic": validProfile(), "two-host": twoHost}
	require.NoError(t, ValidateProfile(twoHost))

	err := ValidateNode(singleSlot(), []v1alpha1.NICOverride{{Slots: []string{"nic0"}, Profile: "two-host"}}, profiles)
	require.ErrorContains(t, err, `slot nic0 (with NIC overrides): configuration eth puts function b0 on PCI device "hostB", which the slot doesn't place`)

	// The same profile in a configuration that uses only the placed device is fine.
	require.NoError(t, ValidateNode(singleSlot(),
		[]v1alpha1.NICOverride{{Slots: []string{"nic0"}, Profile: "two-host", Configuration: "one-host"}}, profiles))

	err = ValidateNode(singleSlot(), []v1alpha1.NICOverride{{Slots: []string{"nic0"}, Configuration: "ib"}}, profiles)
	require.ErrorContains(t, err, `profile nic has no configuration "ib"`)
}

func TestValidateRDMA(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateRDMA(nil))
	require.NoError(t, ValidateRDMA(&v1alpha1.RDMASoftware{UMAD: true, UMADMode: "666"}))
	require.ErrorContains(t, ValidateRDMA(&v1alpha1.RDMASoftware{UMADMode: "rw"}), "umadMode")
}

func TestDecodeIsStrict(t *testing.T) {
	t.Parallel()
	_, err := DecodeProfile([]byte("apiVersion: mokka.nvidia.com/v1alpha1\nkind: SNICProfile\nmetadata: {name: x}\nspec: {identiy: {}}\n"))
	require.ErrorContains(t, err, `unknown field "identiy"`)
	_, err = DecodeProfile([]byte("apiVersion: mokka.nvidia.com/v1alpha1\nkind: SGPURackProfile\nmetadata: {name: x}\n"))
	require.ErrorContains(t, err, "want mokka.nvidia.com/v1alpha1 SNICProfile")
}

// loadChartProfiles decodes every NIC profile the chart ships.
func loadChartProfiles(t *testing.T) Profiles {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(chartNICs, "*.yaml"))
	require.NoError(t, err)
	require.Len(t, paths, 7)
	out := Profiles{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		p, err := DecodeProfile(data)
		require.NoError(t, err, path)
		require.Equal(t, strings.TrimSuffix(filepath.Base(path), ".yaml"), p.Name, "%s: the file is named after its profile", path)
		out[p.Name] = p
	}
	return out
}

// TestShippedProfilesAndRackFixtures decodes every shipped NIC profile and
// every rack fixture strictly into the API types and validates them.
func TestShippedProfilesAndRackFixtures(t *testing.T) {
	t.Parallel()
	profiles := loadChartProfiles(t)

	paths, err := filepath.Glob(filepath.Join(rackFixtures, "*.yaml"))
	require.NoError(t, err)
	require.Len(t, paths, 5)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		r, err := DecodeRackProfile(data)
		require.NoError(t, err, path)
		require.NotEmpty(t, r.Spec.Node.Topology.NICs, path)
		require.NoError(t, ValidateNode(r.Spec.Node.Topology, nil, profiles), path)
		require.NotNil(t, r.Spec.Software, path)
		require.NoError(t, ValidateRDMA(r.Spec.Software.RDMA), path)
	}
}
