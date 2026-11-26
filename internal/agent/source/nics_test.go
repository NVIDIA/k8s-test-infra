// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/nic/declaration"
	nicstate "github.com/NVIDIA/k8s-test-infra/internal/nic/state"
)

const (
	chartDir     = "../../../deployments/nvml-mock/helm/nvml-mock"
	rackFixtures = "../../nic/testdata/rackprofiles"
)

// assembledDocument is a chart GPU profile as the chart renders it into the
// node agent's ConfigMap: the profile, the configuration choices, and the NIC
// profiles its slots name. configurations is a YAML flow sequence.
func assembledDocument(t *testing.T, profile, configurations string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(chartDir, "profiles", profile+".yaml"))
	require.NoError(t, err)
	doc, err := declaration.DecodeDocument(data)
	require.NoError(t, err)
	names := make([]string, 0, len(doc.Slots))
	for _, s := range doc.Slots {
		names = append(names, s.Profile)
	}
	slices.Sort(names)

	var b bytes.Buffer
	b.Write(data)
	fmt.Fprintf(&b, "\nnic_configurations: %s\nnic_profiles:\n", configurations)
	for _, name := range slices.Compact(names) {
		p, err := os.ReadFile(filepath.Join(chartDir, "nics", name+".yaml"))
		require.NoError(t, err)
		j, err := yaml.YAMLToJSON(p)
		require.NoError(t, err)
		fmt.Fprintf(&b, "- %s\n", j)
	}
	return b.Bytes()
}

// helmDocument is the config document `helm template` renders for the chart.
func helmDocument(t *testing.T, args ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed; the assembled document covers the same ground")
	}
	cmd := exec.CommandContext(t.Context(), "helm", append([]string{"template", "t", chartDir,
		"--show-only", "templates/configmap.yaml"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, stderr.String())
	var cm struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, yaml.Unmarshal(out, &cm))
	return []byte(cm.Data["config.yaml"])
}

// resolvedFixture is what the resolver gives the rack fixture's Node, with
// the chart's NIC profiles, for a file-mode node named name.
func resolvedFixture(t *testing.T, rack, name string, configurations string) *nicstate.NICs {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rackFixtures, rack+".yaml"))
	require.NoError(t, err)
	r, err := declaration.DecodeRackProfile(data)
	require.NoError(t, err)
	paths, err := filepath.Glob(filepath.Join(chartDir, "nics", "*.yaml"))
	require.NoError(t, err)
	profiles := declaration.Profiles{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		p, err := declaration.DecodeProfile(data)
		require.NoError(t, err)
		profiles[p.Name] = p
	}
	doc, err := declaration.DecodeDocument([]byte("nic_configurations: " + configurations))
	require.NoError(t, err)
	nics, err := nicstate.Resolve(nicstate.Input{
		Node: nicstate.FileNode(name), Topology: r.Spec.Node.Topology, Configurations: doc.Configurations,
		RDMA: r.Spec.Software.RDMA, Profiles: profiles,
	})
	require.NoError(t, err)
	return nics
}

// documents are both ways a test gets a GPU profile's config document.
func documents(t *testing.T, profile, configurations string, helmArgs ...string) map[string]func() []byte {
	return map[string]func() []byte{
		"assembled": func() []byte { return assembledDocument(t, profile, configurations) },
		"helm": func() []byte {
			return helmDocument(t, append([]string{"--set", "gpu.profile=" + profile}, helmArgs...)...)
		},
	}
}

// TestFileModeNICsMatchTheResolver checks that a file-mode node of a GPU
// profile with NIC slots carries exactly the state the resolver gives the
// matching rack fixture for the node's name, while its infiniband block still
// decides what the ib simulator shows.
func TestFileModeNICsMatchTheResolver(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	for _, c := range []struct {
		profile, rack string
		shown         agent.NetworkShape
	}{
		{"gb300", "gb300", agent.NetworkShape{HCACount: 4, HCAType: "MT4129", LinkLayer: "InfiniBand", RateGbps: 400}},
		{"vrnvl72", "vrnvl72", agent.NetworkShape{HCACount: 12, HCAType: "MT4133", LinkLayer: "Ethernet", RateGbps: 200}},
	} {
		want := resolvedFixture(t, c.rack, "node-a", "[]")
		for source, doc := range documents(t, c.profile, "[]") {
			t.Run(c.profile+"/"+source, func(t *testing.T) {
				state, err := compileState(doc(), filepath.Join(chartDir, "profiles", c.profile+".yaml"))
				require.NoError(t, err)
				require.Empty(t, state.NICError)
				require.Equal(t, *want, state.NICs)

				net := state.NodeShape.Network
				require.True(t, net.IBEnabled)
				shown := agent.NetworkShape{
					HCACount: net.HCACount, HCAType: net.HCAType, LinkLayer: net.LinkLayer, RateGbps: net.RateGbps,
				}
				require.Equal(t, c.shown, shown, "the infiniband block, not the slots, decides what the node shows")
			})
		}
	}
}

// TestFileModeConfigurationSwitch checks that selecting another configuration
// through nics.configurations gives the NIC new functions at the same
// placement.
func TestFileModeConfigurationSwitch(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	before := resolvedFixture(t, "gb300", "node-a", "[]")
	const configurations = "[{slots: [nic0], configuration: eth-multiplane}]"
	for source, doc := range documents(t, "gb300", configurations,
		"--set", "nics.configurations[0].slots[0]=nic0", "--set", "nics.configurations[0].configuration=eth-multiplane") {
		t.Run(source, func(t *testing.T) {
			state, err := compileState(doc(), filepath.Join(chartDir, "profiles", "gb300.yaml"))
			require.NoError(t, err)
			require.Empty(t, state.NICError)
			require.Equal(t, *resolvedFixture(t, "gb300", "node-a", configurations), state.NICs)

			pf0 := function(t, before, "0000:03:00.0")
			for fn := range 4 {
				f := function(t, &state.NICs, fmt.Sprintf("0000:03:00.%d", fn))
				require.Equal(t, "0x020000", f.Class)
				require.Equal(t, pf0.Upstream, f.Upstream)
				require.Equal(t, pf0.Parent, f.Parent)
			}
			require.Equal(t, function(t, before, "0009:03:00.0"), function(t, &state.NICs, "0009:03:00.0"),
				"the Data Direct function stays where it was")
		})
	}
}

func function(t *testing.T, n *nicstate.NICs, addr string) nicstate.Function {
	t.Helper()
	i := slices.IndexFunc(n.Functions, func(f nicstate.Function) bool { return f.Address == addr })
	require.GreaterOrEqual(t, i, 0, "no function at %s", addr)
	return n.Functions[i]
}

// TestInvalidNICDeclarationFailsOpen checks that a NIC declaration that
// doesn't resolve leaves the node's GPUs simulated and says why: a node keeps
// the NICs it last accepted while they still fit its GPUs, a node that never
// accepted any has none, and fixing the declaration recovers on the next
// reload, with no restart.
func TestInvalidNICDeclarationFailsOpen(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	f := NewFileSource(configPath, "", 0, zap.NewNop())
	var hash [32]byte
	load := func(doc []byte) *agent.State {
		t.Helper()
		require.NoError(t, os.WriteFile(configPath, doc, 0o600))
		u := reloadOnce(t, f, &hash)
		require.NotNil(t, u)
		require.NoError(t, u.Err, "a bad NIC declaration must not fail the update")
		require.Len(t, u.State.Devices, 4, "the GPUs are still simulated")
		require.True(t, u.State.NodeShape.Network.IBEnabled, "the infiniband block still drives the node")
		return u.State
	}
	bad := assembledDocument(t, "gb300", "[{slots: [nic0], configuration: eth}]")
	const badReason = `NIC configuration choice 0: profile cx8-c8280z of slot nic0 has no configuration "eth"`

	// A node that never accepted NICs runs without them; a GPU whose parent
	// was a NIC switch port sits below its root port.
	s := load(bad)
	require.True(t, s.NICs.Empty())
	require.False(t, s.NICsKept)
	require.Contains(t, s.NICError, badReason)
	require.Equal(t, "0008:00:00.0", s.NICs.GPUs[0].Parent)

	// Fixing the declaration applies without a restart.
	s = load(assembledDocument(t, "gb300", "[{slots: [nic0], configuration: eth-multiplane}]"))
	require.Empty(t, s.NICError)
	require.Len(t, s.NICs.Instances, 5)
	accepted := s.NICs

	// A node that accepted NICs keeps them through a bad edit.
	s = load(bad)
	require.Contains(t, s.NICError, badReason)
	require.True(t, s.NICsKept)
	require.Equal(t, accepted, s.NICs)
	require.True(t, strings.HasPrefix(s.NICStatus(), "NIC declaration ignored, keeping the NICs last resolved: "))

	// The next declaration that resolves replaces them.
	s = load(assembledDocument(t, "gb300", "[]"))
	require.Empty(t, s.NICError)
	require.False(t, s.NICsKept)
	require.Equal(t, *resolvedFixture(t, "gb300", "node-a", "[]"), s.NICs)
}

// TestKeptNICsMustFitTheNewTree starts from a valid GB300 node and then puts
// an NVSwitch on nic0's network function. The kept NICs would collide with
// it, so the node runs without NICs, its GPUs below their root ports.
func TestKeptNICsMustFitTheNewTree(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	f := NewFileSource(configPath, "", 0, zap.NewNop())
	var hash [32]byte
	good := assembledDocument(t, "gb300", "[]")
	require.NoError(t, os.WriteFile(configPath, good, 0o600))
	u := reloadOnce(t, f, &hash)
	require.Empty(t, u.State.NICError)
	require.False(t, u.State.NICs.Empty())

	collision := bytes.Replace(good, []byte(`bdf: "0000:c3:00.0"`), []byte(`bdf: "0000:03:00.0"`), 1)
	require.NoError(t, os.WriteFile(configPath, collision, 0o600))
	u = reloadOnce(t, f, &hash)
	require.NoError(t, u.Err)
	s := u.State
	require.Contains(t, s.NICError, "PCI address 0000:03:00.0 is both NVSwitch 2 and a function of NIC slot nic0")
	require.False(t, s.NICsKept, "the kept NICs collide with the new NVSwitch too")
	require.True(t, s.NICs.Empty())
	parents := map[string]string{}
	for _, g := range s.NICs.GPUs {
		parents[g.Address] = g.Parent
	}
	require.Equal(t, map[string]string{
		"0008:06:00.0": "0008:00:00.0", "0009:06:00.0": "0009:00:00.0",
		"0018:06:00.0": "0018:00:00.0", "0019:06:00.0": "0019:00:00.0",
	}, parents)
	require.True(t, strings.HasPrefix(s.NICStatus(), "NIC declaration ignored, the node has no NICs until it resolves: "))
}

// TestKeptNICsAreCheckedAsAWholeTree starts from a valid GB300 node and then
// places GPU 0 below a NIC switch port whose children sit on another bus.
// Every parent still exists, but the bus rule fails, so the kept NICs can't
// take that GPU: the node runs without NICs, its GPUs below their root ports.
func TestKeptNICsAreCheckedAsAWholeTree(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	f := NewFileSource(configPath, "", 0, zap.NewNop())
	var hash [32]byte
	good := assembledDocument(t, "gb300", "[]")
	require.NoError(t, os.WriteFile(configPath, good, 0o600))
	require.Empty(t, reloadOnce(t, f, &hash).State.NICError)

	misplaced := bytes.Replace(good, []byte(`0: "0008:05:00.0"`), []byte(`0: "0008:04:00.0"`), 1)
	require.NotEqual(t, good, misplaced)
	require.NoError(t, os.WriteFile(configPath, misplaced, 0o600))
	u := reloadOnce(t, f, &hash)
	require.NoError(t, u.Err)
	s := u.State
	require.Contains(t, s.NICError, "0008:04:00.0")
	require.False(t, s.NICsKept, "the kept NICs can't take GPU 0 where the new document puts it")
	require.True(t, s.NICs.Empty())
	i := slices.IndexFunc(s.NICs.GPUs, func(g nicstate.GPU) bool { return g.Index == 0 })
	require.GreaterOrEqual(t, i, 0)
	require.Equal(t, "0008:00:00.0", s.NICs.GPUs[i].Parent)

	// Restoring the document brings the NICs back.
	require.NoError(t, os.WriteFile(configPath, good, 0o600))
	s = reloadOnce(t, f, &hash).State
	require.Empty(t, s.NICError)
	require.Equal(t, "0008:05:00.0", s.NICs.GPUs[i].Parent)
}

// TestRejectedRootPortsAreNotKept checks the PCI tree of a node that runs
// without NICs: a root port the node just rejected never becomes a GPU's
// parent. A fresh node with no accepted root ports leaves its GPUs without a
// parent; a node that accepted a declaration falls back to its root ports.
func TestRejectedRootPortsAreNotKept(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	good := assembledDocument(t, "gb300", "[]")
	rootPort := []byte(`{address: "0009:00:00.0", rootComplex`)
	parentOf := func(s *agent.State, index int32) string {
		i := slices.IndexFunc(s.NICs.GPUs, func(g nicstate.GPU) bool { return g.Index == index })
		require.GreaterOrEqual(t, i, 0)
		return s.NICs.GPUs[i].Parent
	}
	for _, tc := range []struct{ to, why string }{
		{"0009:06:00.0", "GPU 1's own address"},
		{"0009:01:00.0", "nic0's switch port"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			doc := bytes.Replace(good, rootPort, []byte(`{address: "`+tc.to+`", rootComplex`), 1)
			require.NotEqual(t, good, doc)
			s, err := compileState(doc, filepath.Join(chartDir, "profiles", "gb300.yaml"))
			require.NoError(t, err)
			require.Contains(t, s.NICError, "not on pci0009:00's root bus")
			require.True(t, s.NICs.Empty())
			require.Empty(t, s.NICs.RootPorts, "no root port set holds up")
			for _, g := range s.NICs.GPUs {
				require.Empty(t, g.Parent, "GPU %d", g.Index)
			}
		})
	}

	// A node that accepted the declaration, then gets a bad root port and a
	// GPU parent its kept NICs can't take, falls back to its accepted root
	// ports.
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	f := NewFileSource(configPath, "", 0, zap.NewNop())
	var hash [32]byte
	require.NoError(t, os.WriteFile(configPath, good, 0o600))
	require.Empty(t, reloadOnce(t, f, &hash).State.NICError)
	bad := bytes.Replace(good, rootPort, []byte(`{address: "0009:06:00.0", rootComplex`), 1)
	bad = bytes.Replace(bad, []byte(`1: "0009:05:00.0"`), []byte(`1: "0009:04:00.0"`), 1)
	require.NoError(t, os.WriteFile(configPath, bad, 0o600))
	s := reloadOnce(t, f, &hash).State
	require.NotEmpty(t, s.NICError)
	require.False(t, s.NICsKept)
	require.Equal(t, "0009:00:00.0", parentOf(s, 1))
	require.Equal(t, "0008:00:00.0", parentOf(s, 0))
}

// TestNICDocumentIsStrict checks that a misspelled NIC field is reported, not
// silently dropped.
func TestNICDocumentIsStrict(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	doc := bytes.Replace(assembledDocument(t, "gb300", "[]"), []byte("systemImageGUID: distinct"), []byte("systemImage: distinct"), 1)
	state, err := compileState(doc, filepath.Join(chartDir, "profiles", "gb300.yaml"))
	require.NoError(t, err)
	require.Contains(t, state.NICError, `unknown field "systemImage"`)
}

// TestProfilesWithoutSlotsKeepTheirInfinibandBlock checks that a profile
// without NIC slots has no NIC state and its infiniband block drives the
// network shape, as it does for every profile until #1355.
func TestProfilesWithoutSlotsKeepTheirInfinibandBlock(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	path := filepath.Join(chartDir, "profiles", "gb200.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	state, err := compileState(data, path)
	require.NoError(t, err)
	require.True(t, state.NICs.Empty())
	require.Empty(t, state.NICError)
	require.True(t, state.NodeShape.Network.IBEnabled)
}

// Every shipped GPU profile that declares NIC slots must name NIC profiles the
// chart ships, or its nodes would come up without NICs.
func TestShippedSlotsNameShippedProfiles(t *testing.T) {
	profiles, err := filepath.Glob(filepath.Join(chartDir, "profiles", "*.yaml"))
	require.NoError(t, err)
	for _, path := range profiles {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		doc, err := declaration.DecodeDocument(data)
		require.NoError(t, err, path)
		for _, s := range doc.Slots {
			_, err := os.Stat(filepath.Join(chartDir, "nics", s.Profile+".yaml"))
			require.NoError(t, err, "%s: slot %s", path, s.Slot)
		}
	}
}

// TestFileModeCustomNICProfile checks that a NIC profile from values, such as
// an OEM variant of a shipped card with its own part number and board ID, is
// selected for a slot and resolves like a shipped one.
func TestFileModeCustomNICProfile(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	data, err := os.ReadFile(filepath.Join(chartDir, "nics", "bf3-b3240.yaml"))
	require.NoError(t, err)
	var custom map[string]any
	require.NoError(t, yaml.Unmarshal(data, &custom))
	custom["metadata"] = map[string]any{"name": "bf3-oem"}
	identity := custom["spec"].(map[string]any)["identity"].(map[string]any)
	identity["partNumber"], identity["boardID"] = "OEM-9D3B6-0001", "OEM_0000000001"
	customJSON, err := json.Marshal(custom)
	require.NoError(t, err)

	// The custom profile replaces frontend0's shipped one: through the GPU
	// profile's slots when assembled, and through nics.slots, with the
	// profile's root ports and GPU parents, from values.
	gb300, err := os.ReadFile(filepath.Join(chartDir, "profiles", "gb300.yaml"))
	require.NoError(t, err)
	tree, err := declaration.DecodeDocument(gb300)
	require.NoError(t, err)
	slots := make([]v1alpha1.NICSlot, len(tree.Slots))
	for i := range tree.Slots {
		tree.Slots[i].DeepCopyInto(&slots[i])
		if slots[i].Slot == "frontend0" {
			slots[i].Profile = "bf3-oem"
		}
	}
	values, err := yaml.Marshal(map[string]any{"nics": map[string]any{
		"customProfiles": []any{custom},
		"slots":          slots,
		"rootPorts":      tree.RootPorts,
		"gpuParents":     tree.GPUParents,
	}})
	require.NoError(t, err)
	valuesPath := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(valuesPath, values, 0o600))

	builtIn := resolvedFixture(t, "gb300", "node-a", "[]")
	for source, doc := range map[string]func() []byte{
		"assembled": func() []byte {
			doc := strings.Replace(string(assembledDocument(t, "gb300", "[]")), "profile: bf3-b3240", "profile: bf3-oem", 1)
			return append([]byte(doc), fmt.Sprintf("- %s\n", customJSON)...)
		},
		"helm": func() []byte { return helmDocument(t, "--set", "gpu.profile=gb300", "--values", valuesPath) },
	} {
		t.Run(source, func(t *testing.T) {
			state, err := compileState(doc(), filepath.Join(chartDir, "profiles", "gb300.yaml"))
			require.NoError(t, err)
			require.Empty(t, state.NICError)

			i := slices.IndexFunc(state.NICs.Instances, func(in nicstate.Instance) bool { return in.Slot == "frontend0" })
			require.GreaterOrEqual(t, i, 0)
			require.Equal(t, "bf3-oem", state.NICs.Instances[i].Profile)
			require.Equal(t, "OEM_0000000001", state.NICs.Instances[i].BoardID)

			require.Len(t, state.NICs.Functions, len(builtIn.Functions))
			for i, f := range state.NICs.Functions {
				want := builtIn.Functions[i]
				if f.Slot == "frontend0" {
					require.Equal(t, "OEM-9D3B6-0001", f.VPD["PN"], f.Address)
					want.VPD = maps.Clone(want.VPD)
					want.VPD["PN"] = f.VPD["PN"]
				}
				require.Equal(t, want, f, "only the part number differs from the shipped card")
			}
			require.Len(t, state.NICs.Devices, len(builtIn.Devices))
			for i, d := range state.NICs.Devices {
				want := builtIn.Devices[i]
				if d.Slot == "frontend0" {
					require.Equal(t, "OEM_0000000001", d.BoardID)
					require.Equal(t, "OEM_0000000001", d.Attributes["board_id"])
					want.Attributes = maps.Clone(want.Attributes)
					want.BoardID, want.Attributes["board_id"] = d.BoardID, d.Attributes["board_id"]
				}
				require.Equal(t, want, d, "identifiers follow the slot, not the profile's name")
			}
		})
	}
}

// TestAddressesAreUniqueAcrossGPUsSwitchesAndNICs checks the one check that
// sees every PCI address of a node: an NVSwitch on a NIC function's or a NIC
// switch port's address ignores the NIC declaration, which the engine's check
// of GPUs and switches and the NIC rules' check of GPUs and NICs both miss.
func TestAddressesAreUniqueAcrossGPUsSwitchesAndNICs(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	for _, c := range []struct{ from, to, want string }{
		{`bdf: "0000:c3:00.0"`, `bdf: "0000:03:00.0"`, "PCI address 0000:03:00.0 is both NVSwitch 2 and a function of NIC slot nic0"},
		{`bdf: "0000:c1:00.0"`, `bdf: "0000:01:00.0"`, "PCI address 0000:01:00.0 is both NVSwitch 0 and a switch port of NIC slot nic0"},
	} {
		doc := bytes.Replace(assembledDocument(t, "gb300", "[]"), []byte(c.from), []byte(c.to), 1)
		state, err := compileState(doc, filepath.Join(chartDir, "profiles", "gb300.yaml"))
		require.NoError(t, err, "the engine's own check passes: no GPU shares the address")
		require.Contains(t, state.NICError, c.want)
		require.True(t, state.NICs.Empty())
	}
}

// TestShippedProfilesHaveUniqueAddresses compiles every shipped GPU profile
// and finds no PCI address claimed twice by a GPU, an NVSwitch or a NIC.
func TestShippedProfilesHaveUniqueAddresses(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	paths, err := filepath.Glob(filepath.Join(chartDir, "profiles", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		state, err := compileState(assembledDocument(t, name, "[]"), path)
		require.NoError(t, err, name)
		require.Empty(t, state.NICError, name)
	}
}

// TestRepeatedNICKeyKeepsTheLastValidState edits the ConfigMap by hand, as
// kubectl edit would, to repeat a key inside a NIC slot. The reload reports
// it and keeps the NICs the node accepted before.
func TestRepeatedNICKeyKeepsTheLastValidState(t *testing.T) {
	t.Setenv("NODE_NAME", "node-a")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	f := NewFileSource(configPath, "", 0, zap.NewNop())
	var hash [32]byte
	good := assembledDocument(t, "gb300", "[]")
	require.NoError(t, os.WriteFile(configPath, good, 0o600))
	u := reloadOnce(t, f, &hash)
	require.Empty(t, u.State.NICError)
	accepted := u.State.NICs

	edited := bytes.Replace(good, []byte("      profile: cx8-c8280z\n"), []byte("      profile: cx8-c8280z\n      profile: cx7-hhhl\n"), 1)
	require.NotEqual(t, good, edited)
	require.NoError(t, os.WriteFile(configPath, edited, 0o600))
	u = reloadOnce(t, f, &hash)
	require.NoError(t, u.Err)
	require.Contains(t, u.State.NICError, `key "profile" is repeated`)
	require.True(t, u.State.NICsKept)
	require.Equal(t, accepted, u.State.NICs)
}
