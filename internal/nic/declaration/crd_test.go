// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package declaration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apixv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

const (
	crdDir      = "../../../deployments/mokka-crds/helm/mokka-crds/templates"
	rackExample = "../../../examples/controlplane-crds/sgpu-rack-profile.yaml"
)

// crdValidator validates objects against a generated CRD's schema, as the API
// server does on create.
func crdValidator(t *testing.T, file string) func(obj any) error {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(crdDir, file))
	require.NoError(t, err)
	var crd apixv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	var schema apiextensions.JSONSchemaProps
	require.NoError(t, apixv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &schema, nil))
	validator, _, err := validation.NewSchemaValidator(&schema)
	require.NoError(t, err)

	return func(obj any) error {
		j, err := json.Marshal(obj)
		require.NoError(t, err)
		var u map[string]any
		require.NoError(t, json.Unmarshal(j, &u))
		return validation.ValidateCustomResource(nil, u, validator).ToAggregate()
	}
}

// TestShapesMatchTheCRD keeps the Go rules file mode uses in step with the
// CRDs: every shipped profile passes both, and a field the CRD rejects is
// rejected in file mode too, so a declaration that works in one mode works in
// the other.
func TestShapesMatchTheCRD(t *testing.T) {
	t.Parallel()
	crd := crdValidator(t, "mokka.nvidia.com_snicprofiles.yaml")

	shipped := loadChartProfiles(t)
	for name, p := range shipped {
		require.NoError(t, crd(p), "shipped profile %s", name)
	}

	setType := func(p *v1alpha1.SNICProfile, mutate func(*v1alpha1.NICFunctionType)) {
		ft := p.Spec.FunctionTypes["network"]
		mutate(&ft)
		p.Spec.FunctionTypes["network"] = ft
	}
	port := func(p *v1alpha1.SNICProfile) *v1alpha1.NICPort { return &p.Spec.Configurations[0].Ports[0] }
	function := func(p *v1alpha1.SNICProfile) *v1alpha1.NICFunction { return &p.Spec.Configurations[0].Functions[0] }
	for name, mutate := range map[string]func(*v1alpha1.SNICProfile){
		"model empty":                  func(p *v1alpha1.SNICProfile) { p.Spec.Identity.Model = "" },
		"vendor OUI uppercase":         func(p *v1alpha1.SNICProfile) { p.Spec.Identity.VendorOUI = "0x02C9" },
		"hardware revision without 0x": func(p *v1alpha1.SNICProfile) { p.Spec.Identity.HWRev = "EFA0" },
		"switch port ID uppercase":     func(p *v1alpha1.SNICProfile) { p.Spec.SwitchPort = &v1alpha1.PCIID{VendorID: "15B3", DeviceID: "197c"} },
		"VPD scope unknown": func(p *v1alpha1.SNICProfile) {
			p.Spec.VPD = &v1alpha1.NICVPD{Identities: map[string]v1alpha1.VPDScope{"SN": "chip"}}
		},
		"no function types":   func(p *v1alpha1.SNICProfile) { p.Spec.FunctionTypes = nil },
		"vendor ID uppercase": func(p *v1alpha1.SNICProfile) { setType(p, func(f *v1alpha1.NICFunctionType) { f.VendorID = "15B3" }) },
		"subsystem ID too short": func(p *v1alpha1.SNICProfile) {
			setType(p, func(f *v1alpha1.NICFunctionType) { f.SubsystemDeviceID = "208" })
		},
		"driver empty":       func(p *v1alpha1.SNICProfile) { setType(p, func(f *v1alpha1.NICFunctionType) { f.Driver = "" }) },
		"revision one digit": func(p *v1alpha1.SNICProfile) { setType(p, func(f *v1alpha1.NICFunctionType) { f.Revision = "0x0" }) },
		"RDMA attribute unknown": func(p *v1alpha1.SNICProfile) {
			setType(p, func(f *v1alpha1.NICFunctionType) { f.RDMAOmit = []v1alpha1.RDMAAttribute{"node_guid"} })
		},
		"total VFs negative": func(p *v1alpha1.SNICProfile) { setType(p, func(f *v1alpha1.NICFunctionType) { f.TotalVFs = -1 }) },
		"link width negative": func(p *v1alpha1.SNICProfile) {
			setType(p, func(f *v1alpha1.NICFunctionType) { f.PCIeLink = &v1alpha1.NICPCIeLink{Speed: "x", Width: -1} })
		},
		"link speed empty": func(p *v1alpha1.SNICProfile) {
			setType(p, func(f *v1alpha1.NICFunctionType) { f.PCIeLink = &v1alpha1.NICPCIeLink{Width: 1} })
		},
		"default configuration":   func(p *v1alpha1.SNICProfile) { p.Spec.DefaultConfiguration = "" },
		"no configurations":       func(p *v1alpha1.SNICProfile) { p.Spec.Configurations = nil },
		"technology unknown":      func(p *v1alpha1.SNICProfile) { port(p).Technology = "ethernet" },
		"lane speed unknown":      func(p *v1alpha1.SNICProfile) { port(p).LaneSpeed = "XXX" },
		"width zero":              func(p *v1alpha1.SNICProfile) { port(p).Width = 0 },
		"planes negative":         func(p *v1alpha1.SNICProfile) { port(p).Planes = -1 },
		"capability mask short":   func(p *v1alpha1.SNICProfile) { port(p).CapMask = "0x1" },
		"LMC above 7":             func(p *v1alpha1.SNICProfile) { port(p).LMC = 8 },
		"no functions":            func(p *v1alpha1.SNICProfile) { p.Spec.Configurations[0].Functions = nil },
		"function number above 7": func(p *v1alpha1.SNICProfile) { function(p).Function = 8 },
		"class without 0x":        func(p *v1alpha1.SNICProfile) { function(p).Class = "020000" },
		"PCI device empty":        func(p *v1alpha1.SNICProfile) { function(p).PCIDevice = "" },
		"RDMA device without ports": func(p *v1alpha1.SNICProfile) {
			p.Spec.Configurations[0].RDMADevices[0].Ports = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := validProfile()
			p.APIVersion, p.Kind = v1alpha1.GroupVersion.String(), kindNICProfile
			require.NoError(t, crd(p), "the base profile must pass the CRD")
			mutate(p)
			require.Error(t, crd(p), "the CRD should reject this; if the schema changed, update the case")
			require.Error(t, ValidateProfile(p), "file mode accepts what the CRD rejects")
		})
	}
}

// TestSlotShapesMatchTheCRD does the same for NIC slots and the RDMA software
// in a rack profile.
func TestSlotShapesMatchTheCRD(t *testing.T) {
	t.Parallel()
	crd := crdValidator(t, "mokka.nvidia.com_sgpurackprofiles.yaml")
	profiles := loadChartProfiles(t)

	base := func() *v1alpha1.SGPURackProfile {
		data, err := os.ReadFile(rackExample)
		require.NoError(t, err)
		var r v1alpha1.SGPURackProfile
		require.NoError(t, yaml.UnmarshalStrict(data, &r))
		r.Spec.Software.RDMA = &v1alpha1.RDMASoftware{UMAD: true, UMADMode: "666"}
		r.Spec.Node.Topology.RootPorts = []v1alpha1.RootPort{{Address: "0000:20:00.0", RootComplex: "pci0000:20"}}
		r.Spec.Node.Topology.NICs = []v1alpha1.NICSlot{{
			Slot: "efa0", Profile: "efa-c5n", Board: ptr[int32](0), GPUs: []int32{0},
			Devices: map[string]v1alpha1.NICDevicePlacement{
				"efa": {RootComplex: "pci0000:00", Address: "0000:00:1f", NUMANode: -1},
			},
		}}
		return &r
	}
	goErr := func(r *v1alpha1.SGPURackProfile) error {
		return errors.Join(ValidateNode(r.Spec.Node.Topology, nil, profiles), ValidateRDMA(r.Spec.Software.RDMA))
	}
	r := base()
	require.NoError(t, crd(r))
	require.NoError(t, goErr(r))

	device := func(r *v1alpha1.SGPURackProfile, mutate func(*v1alpha1.NICDevicePlacement)) {
		d := r.Spec.Node.Topology.NICs[0].Devices["efa"]
		mutate(&d)
		r.Spec.Node.Topology.NICs[0].Devices["efa"] = d
	}
	for name, mutate := range map[string]func(*v1alpha1.SGPURackProfile){
		"slot name empty": func(r *v1alpha1.SGPURackProfile) { r.Spec.Node.Topology.NICs[0].Slot = "" },
		"profile empty":   func(r *v1alpha1.SGPURackProfile) { r.Spec.Node.Topology.NICs[0].Profile = "" },
		"board negative":  func(r *v1alpha1.SGPURackProfile) { r.Spec.Node.Topology.NICs[0].Board = ptr[int32](-1) },
		"no devices":      func(r *v1alpha1.SGPURackProfile) { r.Spec.Node.Topology.NICs[0].Devices = nil },
		"root complex malformed": func(r *v1alpha1.SGPURackProfile) {
			device(r, func(d *v1alpha1.NICDevicePlacement) { d.RootComplex = "pci0:00" })
		},
		"address malformed": func(r *v1alpha1.SGPURackProfile) {
			device(r, func(d *v1alpha1.NICDevicePlacement) { d.Address = "0000:0:1f" })
		},
		"NUMA node below -1":  func(r *v1alpha1.SGPURackProfile) { device(r, func(d *v1alpha1.NICDevicePlacement) { d.NUMANode = -2 }) },
		"root port malformed": func(r *v1alpha1.SGPURackProfile) { r.Spec.Node.Topology.RootPorts[0].Address = "0000:01:00" },
		"parent malformed": func(r *v1alpha1.SGPURackProfile) {
			device(r, func(d *v1alpha1.NICDevicePlacement) { d.Parent = "0000:01:00" })
		},
		"GPU parent malformed": func(r *v1alpha1.SGPURackProfile) { r.Spec.Node.Topology.GPUSlots[0].Parent = "0000:01:00" },
		"umad mode not octal":  func(r *v1alpha1.SGPURackProfile) { r.Spec.Software.RDMA.UMADMode = "rw" },
		"root port ID uppercase": func(r *v1alpha1.SGPURackProfile) {
			r.Spec.Node.Topology.RootPorts[0].ID = &v1alpha1.PCIID{VendorID: "10DE", DeviceID: "22b2"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := base()
			mutate(r)
			require.Error(t, crd(r), "the CRD should reject this; if the schema changed, update the case")
			require.Error(t, goErr(r), "file mode accepts what the CRD rejects")
		})
	}
}
