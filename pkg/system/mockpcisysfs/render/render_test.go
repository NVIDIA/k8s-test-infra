// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
)

func TestRender_NoTopologyNoOp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Render(Options{Output: dir}), "Render(no topology)")
	entries, _ := os.ReadDir(dir)
	require.Empty(t, entries, "expected empty output for nil topology")
}

func TestRender_RequiresOutput(t *testing.T) {
	err := Render(Options{Topology: &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}})
	require.Error(t, err, "expected error with empty Output")
}

// TestRender_FullTree exercises the documented sysfs layout end-to-end:
// two GPUs on one root complex must produce a populated devices dir,
// a populated symlink dir, and numa_node files with the configured
// value. This is the regression net for the issue's acceptance test:
// "k8s deviceattribute resolves PCIe root via readlink + path parse".
func TestRender_FullTree(t *testing.T) {
	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{
			{
				ID:       "pci0000:00",
				NUMANode: 0,
				Devices:  []string{"0000:07:00.0", "0000:0F:00.0"},
			},
			{
				ID:       "pci0000:80",
				NUMANode: 1,
				Devices:  []string{"0000:87:00.0"},
			},
		},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}), "Render")

	// numa_node files must contain the per-root NUMA value (and *only*
	// that — a trailing-newline regression would silently bake "0\n0\n"
	// into a re-render).
	mustRead := func(rel, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(dir, rel))
		require.NoError(t, err, "read %s", rel)
		require.Equal(t, want, string(got), "%s", rel)
	}
	mustRead("sys/devices/pci0000:00/0000:07:00.0/numa_node", "0\n")
	mustRead("sys/devices/pci0000:00/0000:0f:00.0/numa_node", "0\n")
	mustRead("sys/devices/pci0000:80/0000:87:00.0/numa_node", "1\n")

	// The symlink target must be *relative* — the deviceattribute
	// library walks readlink() output to extract the pciDDDD:BB
	// component, which only works when the target is the kernel-style
	// "../../../devices/pciDDDD:BB/<bdf>" string.
	mustLink := func(rel, wantTarget string) {
		t.Helper()
		target, err := os.Readlink(filepath.Join(dir, rel))
		require.NoError(t, err, "readlink %s", rel)
		require.Equal(t, wantTarget, target, "%s", rel)
	}
	mustLink("sys/bus/pci/devices/0000:07:00.0", "../../../devices/pci0000:00/0000:07:00.0")
	mustLink("sys/bus/pci/devices/0000:0f:00.0", "../../../devices/pci0000:00/0000:0f:00.0")
	mustLink("sys/bus/pci/devices/0000:87:00.0", "../../../devices/pci0000:80/0000:87:00.0")

	// Resolving the symlink (the path-parse step the deviceattribute
	// library actually performs) must land on the real numa_node file.
	resolved, err := filepath.EvalSymlinks(
		filepath.Join(dir, "sys/bus/pci/devices/0000:07:00.0"))
	require.NoError(t, err, "EvalSymlinks")
	require.True(t, strings.HasSuffix(resolved, "sys/devices/pci0000:00/0000:07:00.0"),
		"resolved=%q does not land under expected root complex", resolved)
}

func TestRender_IdempotentRerender(t *testing.T) {
	dir := t.TempDir()
	topoA := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	// First pass.
	require.NoError(t, Render(Options{Topology: topoA, Output: dir}), "Render pass 1")

	// Second pass with a *different* root complex for the same BDF.
	// Re-render must point the symlink at the new root and overwrite
	// numa_node — a stale symlink would silently misattribute pcieRoot.
	topoB := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:c0", NUMANode: 3,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topoB, Output: dir}), "Render pass 2")

	target, err := os.Readlink(filepath.Join(dir, "sys/bus/pci/devices/0000:07:00.0"))
	require.NoError(t, err, "readlink")
	require.Equal(t, "../../../devices/pci0000:c0/0000:07:00.0", target,
		"symlink target stale across rerender")
	got, err := os.ReadFile(filepath.Join(dir, "sys/devices/pci0000:c0/0000:07:00.0/numa_node"))
	require.NoError(t, err, "read numa_node")
	require.Equal(t, "3\n", string(got), "numa_node not updated")
}

func TestRender_NormalizesUppercaseBDF(t *testing.T) {
	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:BD:00.0"}, // uppercase BDF
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}), "Render")
	// Real sysfs is lowercase. Render must lowercase before writing so
	// downstream tools (lspci, libpciaccess) that string-compare BDFs
	// see what they expect.
	_, err := os.Stat(filepath.Join(dir, "sys/bus/pci/devices/0000:bd:00.0"))
	require.NoError(t, err, "expected lowercase symlink")
}

// --- Config / Validate tests --------------------------------------------------

func TestValidate_AcceptsCanonicalProfile(t *testing.T) {
	var p config.Profile
	require.NoError(t, yaml.Unmarshal([]byte(`
devices:
  - index: 0
    pci:
      bus_id: "0000:07:00.0"
  - index: 1
    pci:
      bus_id: "0000:0F:00.0"
pcie_topology:
  root_complexes:
    - id: "pci0000:00"
      numa_node: 0
      devices:
        - "0000:07:00.0"
        - "0000:0F:00.0"
`), &p), "unmarshal")
	require.NoError(t, p.Validate(), "Validate")
}

func TestValidate_RejectsLegacy8DigitBDF(t *testing.T) {
	// The whole point of the BDF migration is to stop using busIdLegacy
	// form in profile YAMLs. Validation must catch it explicitly so a
	// half-migrated profile doesn't silently render a tree the DRA
	// driver can't resolve.
	p := config.Profile{
		Devices: []config.Device{{Index: 0, PCI: config.PCI{BusID: "00000000:07:00.0"}}},
		PCIeTopology: &config.PCIeTopology{RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"00000000:07:00.0"},
		}}},
	}
	require.Error(t, p.Validate(), "expected error for 8-digit BDF")
}

func TestValidate_RejectsMalformedRoot(t *testing.T) {
	p := config.Profile{
		Devices: []config.Device{{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}}},
		PCIeTopology: &config.PCIeTopology{RootComplexes: []config.RootComplex{{
			ID:      "0000:00", // missing "pci" prefix
			Devices: []string{"0000:07:00.0"},
		}}},
	}
	require.Error(t, p.Validate(), "expected error for malformed root id")
}

func TestValidate_RejectsDuplicateRoot(t *testing.T) {
	p := config.Profile{
		Devices: []config.Device{{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}}},
		PCIeTopology: &config.PCIeTopology{RootComplexes: []config.RootComplex{
			{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:07:00.0"}},
			{ID: "pci0000:00", NUMANode: 1, Devices: []string{}},
		}},
	}
	require.Error(t, p.Validate(), "expected error for duplicate root complex")
}

func TestValidate_RejectsDuplicateBDF(t *testing.T) {
	// A device under two root complexes would silently overwrite its
	// own numa_node on the second pass; catching duplicates at parse
	// time makes the topology unambiguous.
	p := config.Profile{
		Devices: []config.Device{{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}}},
		PCIeTopology: &config.PCIeTopology{RootComplexes: []config.RootComplex{
			{ID: "pci0000:00", NUMANode: 0, Devices: []string{"0000:07:00.0"}},
			{ID: "pci0000:80", NUMANode: 1, Devices: []string{"0000:07:00.0"}},
		}},
	}
	require.Error(t, p.Validate(), "expected error for duplicate BDF")
}

func TestValidate_RejectsUnknownBDF(t *testing.T) {
	// A BDF in the topology that has no matching `devices:` entry
	// indicates a profile typo. Validate must surface it instead of
	// quietly rendering an orphan sysfs entry.
	p := config.Profile{
		Devices: []config.Device{{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}}},
		PCIeTopology: &config.PCIeTopology{RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:99:00.0"},
		}}},
	}
	require.Error(t, p.Validate(), "expected error for unknown BDF")
}

func TestEffectiveTopology_DefaultFlatRoot(t *testing.T) {
	// Profiles that don't carry an explicit `pcie_topology:` block
	// must still produce a renderable, well-formed tree.
	p := config.Profile{Devices: []config.Device{
		{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}},
		{Index: 1, PCI: config.PCI{BusID: "0000:0F:00.0"}},
	}}
	topo := p.EffectiveTopology()
	require.NotNil(t, topo, "expected non-nil default topology")
	require.Len(t, topo.RootComplexes, 1, "expected single default root")
	require.Equal(t, "pci0000:00", topo.RootComplexes[0].ID, "default root id")
	require.Equal(t, 0, topo.RootComplexes[0].NUMANode, "default numa_node")
	require.Len(t, topo.RootComplexes[0].Devices, 2, "default root should contain all devices")
}

func TestEffectiveTopology_PrefersExplicit(t *testing.T) {
	p := config.Profile{
		Devices: []config.Device{{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}}},
		PCIeTopology: &config.PCIeTopology{RootComplexes: []config.RootComplex{{
			ID: "pci0000:80", NUMANode: 1,
			Devices: []string{"0000:07:00.0"},
		}}},
	}
	topo := p.EffectiveTopology()
	require.Equal(t, "pci0000:80", topo.RootComplexes[0].ID, "explicit topology lost")
}
