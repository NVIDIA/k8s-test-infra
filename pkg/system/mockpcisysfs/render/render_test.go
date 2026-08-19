// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/binary"
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

// TestRender_PCIAttributeFiles is the regression net for the lspci fix:
// each rendered device must carry the sysfs identity attribute files that
// libpci reads with die-on-error, plus a binary config space. Without them
// `lspci` fails with "Cannot open .../vendor" inside the mock pod.
func TestRender_PCIAttributeFiles(t *testing.T) {
	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:1A:00.0"},
		}},
	}
	ids := map[string]config.PCI{
		// H100 SXM: device_id 0x233010DE, subsystem_id 0x165810DE.
		"0000:1a:00.0": {BusID: "0000:1A:00.0", DeviceID: 0x233010DE, SubsystemID: 0x165810DE},
	}
	require.NoError(t, Render(Options{Topology: topo, Identities: ids, Output: dir}), "Render")

	devDir := filepath.Join(dir, "sys/devices/pci0000:00/0000:1a:00.0")
	mustRead := func(name, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(devDir, name))
		require.NoError(t, err, "read %s", name)
		require.Equal(t, want, string(got), "%s", name)
	}
	mustRead("vendor", "0x10de\n")
	mustRead("device", "0x2330\n")
	mustRead("subsystem_vendor", "0x10de\n")
	mustRead("subsystem_device", "0x1658\n")
	mustRead("class", "0x030200\n")
	mustRead("revision", "0x00\n")
	mustRead("irq", "0\n")

	// `resource` must match the kernel's 7-row "start end flags" layout so
	// `lspci -v` parses it without erroring.
	resource, err := os.ReadFile(filepath.Join(devDir, "resource"))
	require.NoError(t, err, "read resource")
	lines := strings.Split(strings.TrimRight(string(resource), "\n"), "\n")
	require.Len(t, lines, 7, "resource BAR rows")
	require.Equal(t, "0x0000000000000000 0x0000000000000000 0x0000000000000000", lines[0],
		"resource row format")

	// The binary config space must decode to the same identity so
	// `lspci -x` and the pcilib config-open path agree with the text files.
	cfg, err := os.ReadFile(filepath.Join(devDir, "config"))
	require.NoError(t, err, "read config")
	require.Len(t, cfg, 256, "config space size")
	require.Equal(t, uint16(0x10de), binary.LittleEndian.Uint16(cfg[0x00:]), "config vendor")
	require.Equal(t, uint16(0x2330), binary.LittleEndian.Uint16(cfg[0x02:]), "config device")
	require.Equal(t, byte(0x03), cfg[0x0b], "config class base")
	require.Equal(t, byte(0x02), cfg[0x0a], "config subclass")
	require.Equal(t, uint16(0x10de), binary.LittleEndian.Uint16(cfg[0x2c:]), "config subsystem vendor")
	require.Equal(t, uint16(0x1658), binary.LittleEndian.Uint16(cfg[0x2e:]), "config subsystem device")
}

// TestRender_PCIAttributeFilesDefaultVendor ensures a device present in the
// topology but missing an identity still gets well-formed attribute files
// (NVIDIA vendor default) so lspci never fatals on a missing `vendor`.
func TestRender_PCIAttributeFilesDefaultVendor(t *testing.T) {
	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}), "Render")

	devDir := filepath.Join(dir, "sys/devices/pci0000:00/0000:07:00.0")
	got, err := os.ReadFile(filepath.Join(devDir, "vendor"))
	require.NoError(t, err, "read vendor")
	require.Equal(t, "0x10de\n", string(got), "vendor should default to NVIDIA")
	got, err = os.ReadFile(filepath.Join(devDir, "device"))
	require.NoError(t, err, "read device")
	require.Equal(t, "0x0000\n", string(got), "device without identity")
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

// TestRender_PrunesStaleDevices covers re-profiling a node: an a100 and an
// h100 profile share no BDFs, and the renderer used to only add entries, so
// both sets stayed under the tree and consumers saw their union mounted at
// /sys/bus/pci/devices — more GPUs than the node simulates, some of them
// pointing at a root complex no profile declares.
func TestRender_PrunesStaleDevices(t *testing.T) {
	dir := t.TempDir()
	previous := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: previous, Output: dir}), "Render previous profile")

	current := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:c0", NUMANode: 3,
			Devices: []string{"0000:1a:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: current, Output: dir}), "Render current profile")

	entries, err := os.ReadDir(filepath.Join(dir, "sys/bus/pci/devices"))
	require.NoError(t, err, "read devices dir")
	require.Len(t, entries, 1, "stale device symlinks survived the re-render")
	require.Equal(t, "0000:1a:00.0", entries[0].Name(), "device")

	_, err = os.Stat(filepath.Join(dir, "sys/devices/pci0000:00"))
	require.True(t, os.IsNotExist(err), "stale root complex survived, got err=%v", err)
}

// TestRender_MarkerFollowsTheTree pins the signal consumers gate their bind
// mounts on. The mounted directories are created at the start of a render and
// the DMI attributes are written at its end, so their presence cannot mean
// "complete" — and mounting an incomplete tree fails container creation on a
// bind target that is not there yet.
func TestRender_MarkerFollowsTheTree(t *testing.T) {
	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir}), "Render")
	_, err := os.Stat(filepath.Join(dir, MarkerRelPath))
	require.NoError(t, err, "marker missing after a complete render")

	// A profile with nothing to render must leave no marker: a stale one would
	// keep consumers mounting the previous profile's devices.
	require.NoError(t, Render(Options{Output: dir}), "Render without topology")
	_, err = os.Stat(filepath.Join(dir, MarkerRelPath))
	require.True(t, os.IsNotExist(err), "marker survived an empty render, got err=%v", err)
	entries, err := os.ReadDir(filepath.Join(dir, "sys/bus/pci/devices"))
	require.NoError(t, err, "read devices dir")
	require.Empty(t, entries, "devices survived an empty render")
}

// TestRender_KeepsMountedPathsWhilePruning pins what a re-render must not take
// away. Both mounted directories, and the DMI attributes kind's
// createContainer hook bind-mounts the node's product files onto, are targets
// of mounts already in effect for containers the CDI spec serves — and the CDI
// path cannot wait for the marker, since the runtime applies the spec's mounts
// unconditionally. Removing them mid-render would fail container creation for
// pods that have nothing to do with the re-profiling.
func TestRender_KeepsMountedPathsWhilePruning(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "product_name"), []byte("kind\n"), 0o644), "stage product_name")
	require.NoError(t, os.WriteFile(filepath.Join(src, "product_uuid"), []byte("dead-beef\n"), 0o644), "stage product_uuid")

	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir, DMISource: src}), "Render")

	// Watch the directories across a prune: they must be the same inodes
	// afterwards, since a bind mount whose source was replaced still resolves
	// to the vanished original.
	sysDevices := statOrFail(t, filepath.Join(dir, "sys/devices"))
	pciDevices := statOrFail(t, filepath.Join(dir, "sys/bus/pci/devices"))

	require.NoError(t, pruneTree(dir), "pruneTree")

	require.True(t, os.SameFile(sysDevices, statOrFail(t, filepath.Join(dir, "sys/devices"))),
		"sys/devices replaced by the prune")
	require.True(t, os.SameFile(pciDevices, statOrFail(t, filepath.Join(dir, "sys/bus/pci/devices"))),
		"sys/bus/pci/devices replaced by the prune")
	for _, attr := range []string{"product_name", "product_uuid"} {
		_, err := os.Stat(filepath.Join(dir, "sys/devices/virtual/dmi/id", attr))
		require.NoError(t, err, "%s removed by the prune", attr)
	}
}

func statOrFail(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "stat %s", path)
	return info
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

// TestRender_MirrorsKernelDMI covers the reason the tree carries a DMI
// directory at all: bind-mounting it over /sys/devices hides the real
// virtual/dmi/id, which kind's mount-product-files.sh hook bind-mounts the
// node's product files onto for every container. Both attributes must exist
// as mount targets; only product_name travels by value. product_uuid is a
// node identifier the kernel exposes 0400 to root alone, and kind mounts the
// node's own copy over it anyway, so the tree must not republish it into
// every served container.
func TestRender_MirrorsKernelDMI(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "product_name"), []byte("kind\n"), 0o644), "stage product_name")
	require.NoError(t, os.WriteFile(filepath.Join(src, "product_uuid"), []byte("dead-beef\n"), 0o644), "stage product_uuid")

	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir, DMISource: src}), "Render")

	dmi := filepath.Join(dir, "sys/devices/virtual/dmi/id")
	name, err := os.ReadFile(filepath.Join(dmi, "product_name"))
	require.NoError(t, err, "read product_name")
	require.Equal(t, "kind\n", string(name), "product_name")

	uuid, err := os.ReadFile(filepath.Join(dmi, "product_uuid"))
	require.NoError(t, err, "read product_uuid")
	require.Empty(t, uuid, "product_uuid exists as a mount target, without the node's value")
}

// TestRender_StandsInForUnreadableDMI covers an attribute the renderer cannot
// read — product_name is mode 0444 on every kernel we know of, but a mirror
// that failed on a permission error would leave the mount target missing, and
// mount(8) cannot create one on a read-only sysfs.
func TestRender_StandsInForUnreadableDMI(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any mode; the permission branch is unreachable")
	}
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "product_name"), []byte("secret\n"), 0o000), "stage product_name")

	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir, DMISource: src}), "Render")

	got, err := os.ReadFile(filepath.Join(dir, "sys/devices/virtual/dmi/id/product_name"))
	require.NoError(t, err, "read product_name")
	require.Empty(t, got, "an unreadable attribute renders as an empty stand-in")
}

// TestRender_NoDMIWithoutKernelDMI pins the behavior on kernels that expose
// no DMI at all (Docker Desktop's linuxkit VM, for one): nothing bind-mounts
// product files there, so inventing them would only mislead consumers.
func TestRender_NoDMIWithoutKernelDMI(t *testing.T) {
	dir := t.TempDir()
	topo := &config.PCIeTopology{
		RootComplexes: []config.RootComplex{{
			ID: "pci0000:00", NUMANode: 0,
			Devices: []string{"0000:07:00.0"},
		}},
	}
	require.NoError(t, Render(Options{Topology: topo, Output: dir, DMISource: filepath.Join(t.TempDir(), "absent")}), "Render")

	_, err := os.Stat(filepath.Join(dir, "sys/devices/virtual"))
	require.True(t, os.IsNotExist(err), "expected no DMI directory, got err=%v", err)
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
	// The whole point of the BDF migration is to stop using the 8-digit
	// busId form in profile YAMLs. Validation must catch it explicitly so a
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

func TestDeviceIdentities_InheritsDefaults(t *testing.T) {
	// Per-device entries carry only bus_id; device_id / subsystem_id live
	// under device_defaults.pci. DeviceIdentities must merge them so every
	// BDF resolves to the shared identity.
	var p config.Profile
	require.NoError(t, yaml.Unmarshal([]byte(`
device_defaults:
  pci:
    device_id: 0x233010DE
    subsystem_id: 0x165810DE
devices:
  - index: 0
    pci:
      bus_id: "0000:1A:00.0"
  - index: 1
    pci:
      bus_id: "0000:1B:00.0"
      device_id: 0x232110DE
`), &p), "unmarshal")

	ids := p.DeviceIdentities()
	require.Len(t, ids, 2, "one identity per device with a bus_id")

	// Inherited from defaults (lowercased key).
	require.Equal(t, uint32(0x233010DE), ids["0000:1a:00.0"].DeviceID, "inherited device_id")
	require.Equal(t, uint32(0x165810DE), ids["0000:1a:00.0"].SubsystemID, "inherited subsystem_id")

	// Per-device override wins for device_id, subsystem_id still inherited.
	require.Equal(t, uint32(0x232110DE), ids["0000:1b:00.0"].DeviceID, "overridden device_id")
	require.Equal(t, uint32(0x165810DE), ids["0000:1b:00.0"].SubsystemID, "inherited subsystem_id")
}

func TestDeviceIdentities_NoDefaults(t *testing.T) {
	// A profile without device_defaults still yields one entry per device;
	// the zero identity is fine (the renderer falls back to NVIDIA vendor).
	p := config.Profile{Devices: []config.Device{
		{Index: 0, PCI: config.PCI{BusID: "0000:07:00.0"}},
	}}
	ids := p.DeviceIdentities()
	require.Len(t, ids, 1)
	require.Equal(t, uint32(0), ids["0000:07:00.0"].DeviceID)
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
