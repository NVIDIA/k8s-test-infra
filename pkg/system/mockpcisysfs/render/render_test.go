// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
)

func TestRender_NoTopologyNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := Render(Options{Output: dir}); err != nil {
		t.Fatalf("Render(no topology): %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected empty output for nil topology, got %d entries", len(entries))
	}
}

func TestRender_RequiresOutput(t *testing.T) {
	err := Render(Options{Topology: &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}})
	if err == nil {
		t.Fatal("expected error with empty Output")
	}
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
	if err := Render(Options{Topology: topo, Output: dir}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// numa_node files must contain the per-root NUMA value (and *only*
	// that — a trailing-newline regression would silently bake "0\n0\n"
	// into a re-render).
	mustRead := func(rel, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s: got %q want %q", rel, string(got), want)
		}
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
		if err != nil {
			t.Fatalf("readlink %s: %v", rel, err)
		}
		if target != wantTarget {
			t.Errorf("%s: target=%q want %q", rel, target, wantTarget)
		}
	}
	mustLink("sys/bus/pci/devices/0000:07:00.0", "../../../devices/pci0000:00/0000:07:00.0")
	mustLink("sys/bus/pci/devices/0000:0f:00.0", "../../../devices/pci0000:00/0000:0f:00.0")
	mustLink("sys/bus/pci/devices/0000:87:00.0", "../../../devices/pci0000:80/0000:87:00.0")

	// Resolving the symlink (the path-parse step the deviceattribute
	// library actually performs) must land on the real numa_node file.
	resolved, err := filepath.EvalSymlinks(
		filepath.Join(dir, "sys/bus/pci/devices/0000:07:00.0"))
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if !strings.HasSuffix(resolved, "sys/devices/pci0000:00/0000:07:00.0") {
		t.Errorf("resolved=%q does not land under expected root complex", resolved)
	}
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
	if err := Render(Options{Topology: topoA, Output: dir}); err != nil {
		t.Fatalf("Render pass 1: %v", err)
	}

	// Second pass with a *different* root complex for the same BDF.
	// Re-render must point the symlink at the new root and overwrite
	// numa_node — a stale symlink would silently misattribute pcieRoot.
	topoB := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:c0", NUMANode: 3,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	if err := Render(Options{Topology: topoB, Output: dir}); err != nil {
		t.Fatalf("Render pass 2: %v", err)
	}

	target, err := os.Readlink(filepath.Join(dir, "sys/bus/pci/devices/0000:07:00.0"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "../../../devices/pci0000:c0/0000:07:00.0" {
		t.Errorf("symlink target stale across rerender: got %q", target)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sys/devices/pci0000:c0/0000:07:00.0/numa_node"))
	if err != nil {
		t.Fatalf("read numa_node: %v", err)
	}
	if string(got) != "3\n" {
		t.Errorf("numa_node not updated: got %q", string(got))
	}
}

func TestRender_NormalizesUppercaseBDF(t *testing.T) {
	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:BD:00.0"}, // uppercase BDF
		}},
	}
	if err := Render(Options{Topology: topo, Output: dir}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Real sysfs is lowercase. Render must lowercase before writing so
	// downstream tools (lspci, libpciaccess) that string-compare BDFs
	// see what they expect.
	if _, err := os.Stat(filepath.Join(dir, "sys/bus/pci/devices/0000:bd:00.0")); err != nil {
		t.Errorf("expected lowercase symlink, missing: %v", err)
	}
}

// --- Config / Validate tests --------------------------------------------------

func TestValidate_AcceptsCanonicalProfile(t *testing.T) {
	var p config.Profile
	if err := yaml.Unmarshal([]byte(`
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
`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
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
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for 8-digit BDF, got nil")
	}
}

func TestValidate_RejectsMalformedRoot(t *testing.T) {
	p := config.Profile{
		Devices: []config.Device{{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}}},
		PCIeTopology: &config.PCIeTopology{RootComplexes: []config.RootComplex{{
			ID: "0000:00", // missing "pci" prefix
			Devices: []string{"0000:07:00.0"},
		}}},
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for malformed root id, got nil")
	}
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
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for duplicate BDF, got nil")
	}
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
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for unknown BDF, got nil")
	}
}

func TestEffectiveTopology_DefaultFlatRoot(t *testing.T) {
	// Profiles that don't carry an explicit `pcie_topology:` block
	// must still produce a renderable, well-formed tree.
	p := config.Profile{Devices: []config.Device{
		{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}},
		{Index: 1, PCI: config.PCI{BusID: "0000:0F:00.0"}},
	}}
	topo := p.EffectiveTopology()
	if topo == nil || len(topo.RootComplexes) != 1 {
		t.Fatalf("expected single default root, got %+v", topo)
	}
	if topo.RootComplexes[0].ID != "pci0000:00" {
		t.Errorf("default root id = %q want pci0000:00", topo.RootComplexes[0].ID)
	}
	if topo.RootComplexes[0].NUMANode != 0 {
		t.Errorf("default numa_node = %d want 0", topo.RootComplexes[0].NUMANode)
	}
	if len(topo.RootComplexes[0].Devices) != 2 {
		t.Errorf("default root should contain all devices, got %v", topo.RootComplexes[0].Devices)
	}
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
	if topo.RootComplexes[0].ID != "pci0000:80" {
		t.Errorf("explicit topology lost: %+v", topo)
	}
}
