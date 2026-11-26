// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package conformance checks the NIC declarations Mokka ships against captures
// of real nodes. It resolves each rack fixture's Node into its NIC state,
// lists the state as `mokka-capture nic-surfaces` lists a captured node, and
// compares the listing with the capture through `mokka-capture diff nic`, so a
// declared node is checked as a Mokka node's capture would be. Every
// difference must have a cause in the fixture's causes file.
package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicdiff"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/capture"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/state"
)

const (
	repo         = "../../.."
	chartNICs    = repo + "/deployments/nvml-mock/helm/nvml-mock/nics"
	rackFixtures = "../testdata/rackprofiles"
	// inventoryFixtures select the configurations of rack fixtures that don't
	// run their profiles' defaults.
	inventoryFixtures = "../testdata/inventories"
)

type fixture struct {
	name string
	rack string
	// real is the hardware capture: a node directory collect network wrote,
	// or a listing converted from an older capture.
	real string
	// legacy, when set, is the older capture-node directory the real listing
	// is converted from.
	legacy string
}

var (
	vrNVL72 = fixture{
		name:   "vr-nvl72",
		rack:   "vrnvl72",
		real:   "tests/hardware-captures/vr-nvl72/node-a/nic-surfaces.tsv",
		legacy: "tests/hardware-captures/vr-nvl72/node-a",
	}
	gb300IB = fixture{
		name: "gb300-ib",
		rack: "gb300",
		real: "tests/hardware-captures/gb300-ib/node-a",
	}
	gb300RoCE = fixture{
		name: "gb300-roce",
		rack: "gb300-cx7",
		real: "tests/hardware-captures/gb300-roce/node-a",
	}
	awsGB300 = fixture{
		name: "aws-gb300",
		rack: "aws-gb300",
		real: "internal/nic/conformance/testdata/converted/aws-gb300-vf.nic-surfaces.tsv",
	}
	awsEFA = fixture{
		name: "aws-c5n-efa",
		rack: "aws-c5n",
		real: "internal/nic/conformance/testdata/converted/aws-c5n-efa.nic-surfaces.tsv",
	}
	fixtures = []fixture{vrNVL72, gb300IB, gb300RoCE, awsGB300, awsEFA}
)

// check and summary mirror the JSON `diff nic -json` writes.
type check struct {
	Status string `json:"status"`
	Rule   string `json:"rule"`
	Object string `json:"object"`
	Real   string `json:"real"`
	Sim    string `json:"sim"`
	Detail string `json:"detail"`
	Cause  string `json:"cause"`
}

type summary struct {
	Checks      int            `json:"checks"`
	Passed      int            `json:"passed"`
	Failed      map[string]int `json:"failed"`
	NotCompared map[string]int `json:"not_compared"`
}

// diffNIC runs `mokka-capture diff nic -json` and returns its checks, its
// summary and what it wrote to standard error.
func diffNIC(t *testing.T, realPath, simPath, causes string) ([]check, summary, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := &cli.Command{
		Name:           "diff",
		Writer:         &out,
		ErrWriter:      &errOut,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands:       []*cli.Command{nicdiff.Command()},
	}
	args := []string{"diff", "nic", "-json"}
	if causes != "" {
		args = append(args, "-causes", causes)
	}
	// diff nic exits non-zero on an unclassified failure; the checks say why.
	_ = root.Run(context.Background(), append(args, realPath, simPath))

	var checks []check
	var s summary
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case bytes.Contains(line, []byte(`"status"`)):
			var c check
			require.NoError(t, json.Unmarshal(line, &c))
			checks = append(checks, c)
		case bytes.Contains(line, []byte(`"summary"`)):
			var wrapped struct {
				Summary summary `json:"summary"`
			}
			require.NoError(t, json.Unmarshal(line, &wrapped))
			s = wrapped.Summary
		}
	}
	require.NotZero(t, s.Checks, "diff nic wrote no summary; stderr: %s", errOut.String())
	return checks, s, errOut.String()
}

// declared is one fixture's Node: its rack fragment and the shipped profiles.
type declared struct {
	rack *v1alpha1.SGPURackProfile
	// choices are the configuration choices of the rack group that runs the
	// rack, from its inventory fixture; none runs the profiles' defaults.
	choices  []v1alpha1.NICConfigurationChoice
	profiles declaration.Profiles
}

func load(t *testing.T, fx fixture) declared {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(chartNICs, "*.yaml"))
	require.NoError(t, err)
	profiles := declaration.Profiles{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		p, err := declaration.DecodeProfile(data)
		require.NoError(t, err, path)
		profiles[p.Name] = p
	}
	data, err := os.ReadFile(filepath.Join(rackFixtures, fx.rack+".yaml"))
	require.NoError(t, err)
	rack, err := declaration.DecodeRackProfile(data)
	require.NoError(t, err)
	return declared{rack: rack, choices: inventoryChoices(t, fx.rack), profiles: profiles}
}

// inventoryChoices are the configuration choices the inventory fixture of a
// rack makes, or none when it has none.
func inventoryChoices(t *testing.T, rack string) []v1alpha1.NICConfigurationChoice {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(inventoryFixtures, rack+".yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var inv v1alpha1.SGPUInventory
	require.NoError(t, yaml.UnmarshalStrict(data, &inv))
	for _, g := range inv.Spec.RackGroups {
		if g.ProfileRef.Name == rack {
			return g.NICConfigurations
		}
	}
	return nil
}

// surfacesOf resolves the declared Node and lists its NIC surfaces in a
// temporary file, as nic-surfaces lists a captured node.
func surfacesOf(t *testing.T, d declared) string {
	t.Helper()
	spec := &d.rack.Spec
	var rdma *v1alpha1.RDMASoftware
	if spec.Software != nil {
		rdma = spec.Software.RDMA
	}
	nics, err := state.Resolve(state.Input{
		Node: state.FileNode(d.rack.Name), Topology: spec.Node.Topology, Configurations: d.choices, RDMA: rdma,
		Profiles: d.profiles,
	})
	require.NoError(t, err)
	var listing bytes.Buffer
	require.NoError(t, view(nics, spec.Node.GPUs).Write(&listing))
	path := filepath.Join(t.TempDir(), "nic-surfaces.tsv")
	require.NoError(t, os.WriteFile(path, listing.Bytes(), 0o600))
	return path
}

// TestConformance compares each fixture's declared Node with its real capture.
// Every failure must have a cause in testdata/<fixture>.nic-causes.yaml, none
// may be a rule bug, and every cause must still match a failure.
func TestConformance(t *testing.T) {
	t.Parallel()
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			causes := filepath.Join("testdata", fx.name+".nic-causes.yaml")
			sim := surfacesOf(t, load(t, fx))
			// NIC_GENERATED_DIR=<dir> keeps the declared node's surfaces as
			// <dir>/<fixture>.tsv, for mokka-capture diff nic by hand.
			if dir := os.Getenv("NIC_GENERATED_DIR"); dir != "" {
				data, err := os.ReadFile(sim)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, fx.name+".tsv"), data, 0o600))
			}
			checks, s, stderr := diffNIC(t, filepath.Join(repo, fx.real), sim, causes)

			var problems []string
			for _, c := range checks {
				if c.Status == "FAIL" && (c.Cause == "unclassified" || c.Cause == "rule bug") {
					problems = append(problems, fmt.Sprintf("%s %s %s: real=%q declared=%q %s",
						c.Cause, c.Rule, c.Object, c.Real, c.Sim, c.Detail))
				}
			}
			if strings.TrimSpace(stderr) != "" {
				problems = append(problems, strings.TrimSpace(stderr))
			}
			t.Logf("%s: %d checks, %d passed; failed %s; not compared %s",
				fx.name, s.Checks, s.Passed, counts(s.Failed), counts(s.NotCompared))
			require.Empty(t, problems, "\n"+strings.Join(problems, "\n"))
		})
	}
}

func counts(m map[string]int) string {
	var out []string
	for k, v := range m {
		if v > 0 {
			out = append(out, fmt.Sprintf("%s %d", k, v))
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// TestConvertedCapturesAreCurrent keeps each listing converted from an older
// capture in step with its converter. UPDATE=1 rewrites them.
func TestConvertedCapturesAreCurrent(t *testing.T) {
	t.Parallel()
	for _, fx := range fixtures {
		if fx.legacy == "" {
			continue
		}
		r, err := capture.ConvertNodeCapture(filepath.Join(repo, fx.legacy))
		require.NoError(t, err)
		var want bytes.Buffer
		require.NoError(t, r.Write(&want))
		path := filepath.Join(repo, fx.real)
		if os.Getenv("UPDATE") != "" {
			require.NoError(t, os.WriteFile(path, want.Bytes(), 0o600))
			continue
		}
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, want.String(), string(got), "%s is stale; rerun with UPDATE=1", fx.real)
	}
}

func setDevice(r *v1alpha1.SGPURackProfile, slot int, name string, mutate func(*v1alpha1.NICDevicePlacement)) {
	d, _ := r.Spec.Node.Topology.NICs[slot].Device(name)
	mutate(d)
}

// rootPortOf is the root port above a device's switch: the parent of the
// switch port that has no parent among the others.
func rootPortOf(d *v1alpha1.NICDevicePlacement) string {
	ports := map[string]bool{}
	for _, b := range d.Bridges {
		ports[b.Address] = true
	}
	for _, b := range d.Bridges {
		if !ports[b.Parent] {
			return b.Parent
		}
	}
	return d.Parent
}

func setFunctionType(p *v1alpha1.SNICProfile, name string, mutate func(*v1alpha1.NICFunctionType)) {
	ft, _ := p.Spec.FunctionType(name)
	mutate(ft)
}

// TestMutationsAreCaught checks that the comparison isn't vacuous: each wrong
// declaration must fail the checks it affects.
func TestMutationsAreCaught(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		fixture fixture
		profile string
		mutate  func(*v1alpha1.SNICProfile, *v1alpha1.SGPURackProfile)
		want    []string // "rule object"
	}{
		{
			name: "two lanes instead of one", fixture: vrNVL72, profile: "cx9-vr",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				p.Spec.Configurations[0].Ports[0].Width = 2
			},
			want: []string{"port.rate port/0001:03:00.0/dev/1", "net.speed net/0001:03:00.0/p0"},
		},
		{
			name: "InfiniBand class", fixture: vrNVL72, profile: "cx9-vr",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				p.Spec.Configurations[0].Functions[0].Class = "0x020700"
			},
			want: []string{"pci.class pci/0003:01:00.0"},
		},
		{
			name: "slot without its switch", fixture: vrNVL72, profile: "cx9-vr",
			mutate: func(_ *v1alpha1.SNICProfile, r *v1alpha1.SGPURackProfile) {
				setDevice(r, 0, "network", func(d *v1alpha1.NICDevicePlacement) { d.Bridges, d.Parent = nil, rootPortOf(d) })
			},
			want: []string{"pci.upstream pci/0001:03:00.0", "bridge.device bridge/0001:01:00.0"},
		},
		{
			name: "umad loaded", fixture: vrNVL72, profile: "cx9-vr",
			mutate: func(_ *v1alpha1.SNICProfile, r *v1alpha1.SGPURackProfile) {
				r.Spec.Software.RDMA.UMAD = true
			},
			want: []string{"node.dev_infiniband node", "node.mad_abi_version node"},
		},
		{
			name: "wrong NUMA node", fixture: vrNVL72, profile: "cx9-vr",
			mutate: func(_ *v1alpha1.SNICProfile, r *v1alpha1.SGPURackProfile) {
				setDevice(r, 4, "network", func(d *v1alpha1.NICDevicePlacement) { d.NUMANode = 0 })
			},
			want: []string{"pci.numa_node pci/0009:03:00.0"},
		},
		{
			name: "wrong firmware", fixture: vrNVL72, profile: "cx9-vr",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				p.Spec.Identity.FirmwareVersion = "82.48.0000"
			},
			want: []string{"rdma.fw_ver rdma/0001:03:00.0/dev", "ibv.fw_ver ibv/0001:03:00.0/dev", "net.ethtool.firmware-version net/0001:03:00.0/p0"},
		},
		{
			name: "Data Direct paired with the same-side GPU", fixture: gb300IB, profile: "cx8-c8280z",
			mutate: func(_ *v1alpha1.SNICProfile, r *v1alpha1.SGPURackProfile) {
				nics := r.Spec.Node.Topology.NICs
				a, _ := nics[0].Device("dataDirect")
				b, _ := nics[1].Device("dataDirect")
				*a, *b = *b, *a
			},
			want: []string{"rel.data_direct.by_v3 rel/data_direct/0000:03:00.0", "rel.data_direct.gpu rel/data_direct/0002:03:00.0"},
		},
		{
			name: "GPU on the other NUMA node", fixture: gb300IB, profile: "cx8-c8280z",
			mutate: func(_ *v1alpha1.SNICProfile, r *v1alpha1.SGPURackProfile) {
				r.Spec.Node.Topology.GPUSlots[0].NumaNode = 1
			},
			want: []string{"gpu.numa_node gpu/0008:06:00.0", "topo.GPU0 topo/0000:03:00.0/dev"},
		},
		{
			name: "planes dropped", fixture: gb300IB, profile: "cx8-c8280z",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				p.Spec.Configurations[0].Ports[0].Planes = 0
			},
			want: []string{"mad.name mad/0000:03:00.0/dev/1/issm"},
		},
		{
			name: "system image GUID equal on every ASIC", fixture: gb300IB, profile: "cx8-c8280z",
			mutate: func(_ *v1alpha1.SNICProfile, r *v1alpha1.SGPURackProfile) {
				r.Spec.Node.Topology.NICs[1].SystemImageGUID = ""
			},
			want: []string{"rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0002:03:00.0"},
		},
		{
			name: "serial per ASIC instead of per board", fixture: gb300IB, profile: "cx8-c8280z",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				p.Spec.VPD.Identities["SN"] = v1alpha1.VPDScopeASIC
			},
			want: []string{"rel.board.shares_serial_with rel/board/0000:03:00.0"},
		},
		{
			name: "BlueField-4 system image GUID from host link A", fixture: vrNVL72, profile: "bf4-vr",
			mutate: func(_ *v1alpha1.SNICProfile, r *v1alpha1.SGPURackProfile) {
				r.Spec.Node.Topology.NICs[8].SystemImageGUID = "rdma-a0"
			},
			want: []string{
				"rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/0005:41:00.0",
				"rel.sys_image_guid.is_node_guid_of rel/sys_image_guid/000d:41:00.0",
			},
		},
		{
			name: "BlueField-3 SoC function with a VU", fixture: gb300IB, profile: "bf3-b3240",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				setFunctionType(p, "socManagement", func(ft *v1alpha1.NICFunctionType) { ft.VPDOmit = nil })
			},
			want: []string{"pci.vpd.VU pci/0016:01:00.2"},
		},
		{
			name: "ConnectX-7 ports both on one host link", fixture: gb300RoCE, profile: "cx7-hhhl",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				c := &p.Spec.Configurations[0]
				c.Functions[1].PCIDevice, c.Functions[1].Function = "hostA", 1
			},
			want: []string{"pci.device pci/0017:01:00.0", "pci.device pci/0007:01:00.1"},
		},
		{
			name: "EFA declared as RoCE", fixture: awsEFA, profile: "efa-c5n",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				p.Spec.Configurations[0].Ports[0].Technology = v1alpha1.NICTechnologyRoCE
			},
			want: []string{"port.link_layer port/0000:00:1f.0/dev/1", "ibv.transport ibv/0000:00:1f.0/dev"},
		},
		{
			name: "cloud VFs without their Data Direct function", fixture: awsGB300, profile: "cx8-aws-vf",
			mutate: func(p *v1alpha1.SNICProfile, _ *v1alpha1.SGPURackProfile) {
				c := &p.Spec.Configurations[0]
				c.Functions = c.Functions[:2]
			},
			want: []string{"pci.device pci/0000:9c:00.0", "pci.device pci/0000:c4:00.0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := load(t, tc.fixture)
			tc.mutate(d.profiles[tc.profile], d.rack)
			checks, _, _ := diffNIC(t, filepath.Join(repo, tc.fixture.real), surfacesOf(t, d), "")
			failed := map[string]bool{}
			for _, c := range checks {
				if c.Status == "FAIL" {
					failed[c.Rule+" "+c.Object] = true
				}
			}
			for _, w := range tc.want {
				require.True(t, failed[w], "expected %q to fail", w)
			}
		})
	}
}
