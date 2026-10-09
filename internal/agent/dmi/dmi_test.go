// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package dmi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/sysattr"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
)

const gb300Name = "NVIDIA GB300 NVL"

func gb300State() *agent.State {
	return &agent.State{
		Devices: []agent.DeviceSpec{
			{Name: gb300Name, PCIBusID: "0000:01:00.0"},
			{Name: gb300Name, PCIBusID: "0008:01:00.0"},
		},
		NodeShape: agent.NodeShape{Topology: agent.PCIeTopology{RootComplexes: []agent.RootComplex{
			{ID: "pci0000:00", NUMANode: 0, DeviceBDFs: []string{"0000:01:00.0"}},
			{ID: "pci0008:00", NUMANode: 1, DeviceBDFs: []string{"0008:01:00.0"}},
		}}},
	}
}

func readNode(t *testing.T, entry string) sysattr.Attributes {
	t.Helper()
	attrs, err := sysattr.Read(filepath.Join(entry, "id"))
	require.NoError(t, err)
	return attrs
}

// ─── The node's view ─────────────────────────────────────────────────────────

// NFD v0.19 reads these sixteen, and logs an error for each one missing.
func TestStageNode_ServesEveryAttributeNFDReads(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()

	require.NoError(t, stageNode(entry, gb300State()))

	require.Equal(t, sysattr.Attributes{
		"bios_date":         "",
		"bios_vendor":       "NVIDIA",
		"bios_version":      "",
		"board_asset_tag":   "",
		"board_name":        gb300Name,
		"board_vendor":      "NVIDIA",
		"board_version":     "",
		"chassis_asset_tag": "",
		"chassis_type":      "",
		"chassis_vendor":    "NVIDIA",
		"chassis_version":   "",
		"product_family":    "",
		"product_name":      gb300Name,
		"product_sku":       "",
		"product_version":   "",
		"sys_vendor":        "NVIDIA",
	}, readNode(t, entry))
}

// kind's createContainer hook bind-mounts its own product_uuid into every
// container once the node shows one, and a container's own sysfs has no
// target for it: serving product_uuid would stop every container on the node.
func TestStageNode_NeverServesProductUUID(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()

	require.NoError(t, stageNode(entry, gb300State()))

	require.NoFileExists(t, filepath.Join(entry, "id/product_uuid"))
}

func TestStageNode_LeavesNamesEmptyWithoutADeviceName(t *testing.T) {
	t.Parallel()
	entry := t.TempDir()

	require.NoError(t, stageNode(entry, &agent.State{}))

	attrs := readNode(t, entry)
	require.Empty(t, attrs["product_name"])
	require.Equal(t, "NVIDIA", attrs["sys_vendor"])
}

// ─── The served tree's copy ──────────────────────────────────────────────────

// writeKernelDMI fakes a kernel that exposes DMI, at the path
// /sys/class/dmi/id resolves into.
func writeKernelDMI(t *testing.T, h *host.Host, attrs map[string]string) string {
	t.Helper()
	dir := New(h, false).kernel
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, val := range attrs {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(val), 0o444))
	}
	return dir
}

func served(h *host.Host) string { return New(h, false).served }

func TestSimulator_MirrorsKernelProductNameIntoServedTree(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	writeKernelDMI(t, h, map[string]string{"product_name": "NVIDIA DGX A100\n"})

	require.NoError(t, New(h, true).Stage(t.Context(), gb300State()))

	attrs, err := sysattr.Read(served(h))
	require.NoError(t, err)
	require.Equal(t, "NVIDIA DGX A100", attrs["product_name"], "the kernel's identity wins over the simulated one")
}

// kind's hook mounts the node's product files into every container, so the
// served tree must hold their targets whether or not DMI is simulated.
func TestSimulator_MirrorsKernelDMIEvenWhenNotSimulating(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	writeKernelDMI(t, h, map[string]string{"product_name": "NVIDIA DGX A100\n"})

	require.NoError(t, New(h, false).Stage(t.Context(), gb300State()))

	require.FileExists(t, filepath.Join(served(h), "product_name"))
	require.FileExists(t, filepath.Join(served(h), "product_uuid"))
}

// product_uuid identifies the node and kind mounts its own copy over ours, so
// mirroring the value would republish it into every served container for no gain.
func TestSimulator_ServedProductUUIDIsAnEmptyStandIn(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	writeKernelDMI(t, h, map[string]string{
		"product_name": "NVIDIA DGX A100\n",
		"product_uuid": "4c4c4544-0037-5710-8058-b7c04f503432\n",
	})

	require.NoError(t, New(h, true).Stage(t.Context(), gb300State()))

	attrs, err := sysattr.Read(served(h))
	require.NoError(t, err)
	require.Empty(t, attrs["product_uuid"], "the node's UUID must not travel into served containers")

	info, err := os.Stat(filepath.Join(served(h), "product_uuid"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o400), info.Mode().Perm(), "mirror the kernel's own permissions")
}

// An attribute the agent cannot read still has to exist, because it is kind's
// bind-mount target and mount(8) cannot create one on a read-only sysfs.
func TestSimulator_StandsInForUnreadableProductName(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	h := host.New(t.TempDir())
	dir := writeKernelDMI(t, h, map[string]string{"product_name": "NVIDIA DGX A100\n"})
	require.NoError(t, os.Chmod(filepath.Join(dir, "product_name"), 0o000))

	require.NoError(t, New(h, true).Stage(t.Context(), gb300State()), "an unreadable attribute is not a staging failure")

	attrs, err := sysattr.Read(served(h))
	require.NoError(t, err)
	require.Contains(t, attrs, "product_name")
	require.Empty(t, attrs["product_name"])
}

// A served container sees the identity the node shows, from the first pass:
// it must not depend on whether the node's overmount is already up.
func TestSimulator_ServesTheSimulatedIdentityWithoutKernelDMI(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())

	s := New(h, true)
	require.NoError(t, s.Stage(t.Context(), gb300State()))

	copied, err := sysattr.Read(s.served)
	require.NoError(t, err)
	require.Equal(t, readNode(t, s.overlay.StagedEntry()), copied)
	require.NotContains(t, copied, "product_uuid")
}

func TestSimulator_ServesNothingWithoutKernelDMIOrSimulation(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())

	s := New(h, false)
	require.NoError(t, s.Stage(t.Context(), gb300State()))

	require.NoDirExists(t, s.served)
	require.NoDirExists(t, s.overlay.Staged, "the node's view is left alone")
}

// The copy exists only for containers the tree is served to, and nothing is
// served when no tree is rendered.
func TestSimulator_ServesNothingWithoutTopology(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	writeKernelDMI(t, h, map[string]string{"product_name": "NVIDIA DGX A100\n"})

	require.NoError(t, New(h, true).Stage(t.Context(), &agent.State{}))

	require.NoDirExists(t, h.RootPath("sys"), "sys/ must not be created when state has no root complexes")
}

func TestSimulator_DiscardRemovesTheServedCopy(t *testing.T) {
	t.Parallel()
	h := host.New(t.TempDir())
	writeKernelDMI(t, h, map[string]string{"product_name": "NVIDIA DGX A100\n"})
	s := New(h, false)
	require.NoError(t, s.Stage(t.Context(), gb300State()))

	require.NoError(t, s.Discard(t.Context()))

	require.NoDirExists(t, served(h))
}
