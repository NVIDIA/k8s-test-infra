// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package render writes a fake InfiniBand sysfs tree from an
// [config.Infiniband] specification.
//
// Layout matches what the kernel ib_core driver exposes at runtime, so real
// userspace tools (ibstat, ibstatus, iblinkinfo, libibverbs consumers, ...)
// can read it through libibmocksys.so's path redirection.
package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
)

// Options controls a single rendering pass.
type Options struct {
	IB       config.Infiniband
	GPUCount int    // used when IB.HCACountOverride == 0
	NodeName string // expanded into NodeDescTemplate
	Output   string // fake-root directory; subtree rooted at <Output>/sys/class/...
}

// Render writes the entire tree. It is idempotent: existing files are
// truncated and rewritten, existing directories are reused.
func Render(o Options) error {
	if !o.IB.Enabled {
		return nil
	}
	ib := o.IB.Defaults()
	guidPrefix := normalizeGUIDPrefix(ib.GUIDPrefix)
	if len(guidPrefix) < 8 || !isHexString(guidPrefix) {
		return fmt.Errorf("guid_prefix must be at least 8 hex digits after stripping ':' (got %q -> %q)", ib.GUIDPrefix, guidPrefix)
	}

	hcaCount := ib.HCACountOverride
	if hcaCount <= 0 {
		hcaCount = o.GPUCount * ib.HCAsPerGPU
	}
	if hcaCount <= 0 {
		return fmt.Errorf("infiniband: hca_count is 0 (gpu_count=%d, hcas_per_gpu=%d)", o.GPUCount, ib.HCAsPerGPU)
	}
	if hcaCount > maxGUIDHCAs {
		return fmt.Errorf("infiniband: hca_count=%d exceeds mock GUID capacity %d", hcaCount, maxGUIDHCAs)
	}
	if hcaCount > maxUnicastLIDs {
		return fmt.Errorf("infiniband: hca_count=%d exceeds mock LID capacity %d", hcaCount, maxUnicastLIDs)
	}

	root := o.Output
	if err := mkdirAll(root, "sys/class/infiniband"); err != nil {
		return err
	}
	if err := mkdirAll(root, "sys/class/infiniband_mad"); err != nil {
		return err
	}
	if err := mkdirAll(root, "sys/class/infiniband_verbs"); err != nil {
		return err
	}
	if err := mkdirAll(root, "dev/infiniband"); err != nil {
		return err
	}

	if err := writeFile(root, "sys/class/infiniband_mad/abi_version", "5\n"); err != nil {
		return err
	}
	if err := writeFile(root, "sys/class/infiniband_verbs/abi_version", "6\n"); err != nil {
		return err
	}

	for i := 0; i < hcaCount; i++ {
		if err := renderHCA(root, ib, guidPrefix, i, hcaCount, o.NodeName); err != nil {
			return fmt.Errorf("rendering mlx5_%d: %w", i, err)
		}
	}
	return nil
}

func renderHCA(root string, ib config.Infiniband, guidPrefix string, idx, hcaCount int, nodeName string) error {
	caName := fmt.Sprintf("mlx5_%d", idx)
	caDir := filepath.Join("sys/class/infiniband", caName)
	if err := mkdirAll(root, caDir); err != nil {
		return err
	}

	nid := nodeID(nodeName)
	guid := perHCAGUID(guidPrefix, nid, idx)
	portGUID := perHCAPortGUID(guidPrefix, nid, idx)
	nodeDesc := strings.NewReplacer(
		"{node_name}", nodeName,
		"{idx}", fmt.Sprintf("%d", idx),
	).Replace(ib.NodeDescTemplate)

	// Slice (not map) so file creation order is deterministic — useful when
	// diffing rendered trees across runs and when reasoning about partial
	// failures from later in the function.
	caFiles := []nameValue{
		{"node_type", "1: CA\n"},
		{"node_guid", guid + "\n"},
		{"sys_image_guid", guid + "\n"},
		{"fw_ver", ib.FWVersion + "\n"},
		{"hw_rev", ib.HWRev + "\n"},
		{"board_id", ib.BoardID + "\n"},
		{"hca_type", ib.HCAType + "\n"},
		{"node_desc", nodeDesc + "\n"},
	}
	for _, f := range caFiles {
		if err := writeFile(root, filepath.Join(caDir, f.name), f.value); err != nil {
			return err
		}
	}

	portDir := filepath.Join(caDir, "ports/1")
	if err := mkdirAll(root, portDir); err != nil {
		return err
	}
	if err := mkdirAll(root, filepath.Join(portDir, "gids")); err != nil {
		return err
	}
	if err := mkdirAll(root, filepath.Join(portDir, "pkeys")); err != nil {
		return err
	}
	if err := mkdirAll(root, filepath.Join(portDir, "counters")); err != nil {
		return err
	}
	// In real Linux sysfs `gid_attrs` is a directory containing per-GID
	// attribute files (ndevs, types, ...). Create it as a directory so
	// libibverbs / iblinkinfo opendir() succeeds; ibstat doesn't read it.
	if err := mkdirAll(root, filepath.Join(portDir, "gid_attrs")); err != nil {
		return err
	}

	portFiles := []nameValue{
		{"state", formatPortState(ib.PortState)},
		{"phys_state", formatPhysState(ib.PhysState)},
		{"rate", formatRate(ib.RateGbps) + "\n"},
		{"lid", fmt.Sprintf("0x%04x\n", hcaLID(nid, idx, hcaCount))},
		{"lid_mask_count", "0\n"},
		{"sm_lid", "0x0001\n"},
		{"sm_sl", "0\n"},
		{"cap_mask", "0x2651e848\n"},
		{"link_layer", ib.LinkLayer + "\n"},
		// Real mlx5 ports expose port_guid alongside node_guid (typically
		// differing only in the U/L bit). Surface it so tools that key off
		// per-port identity (e.g. perftest, NCCL topology probes) work.
		{"port_guid", portGUID + "\n"},
	}
	for _, f := range portFiles {
		if err := writeFile(root, filepath.Join(portDir, f.name), f.value); err != nil {
			return err
		}
	}

	// gids/0 lower 64 bits must equal the port GUID (fe80:: + port GUID).
	portLower := hcaIdentity(nid, idx) | 1
	gid := fmt.Sprintf("fe80:0000:0000:0000:%s:%s:%04x:%04x",
		guidPrefix[0:4], guidPrefix[4:8], portLower>>16, portLower&0xffff)
	if err := writeFile(root, filepath.Join(portDir, "gids/0"), gid+"\n"); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(portDir, "pkeys/0"), "0xffff\n"); err != nil {
		return err
	}

	// Counters that diag tools optionally read.
	zeroCounters := []string{
		"port_xmit_data", "port_rcv_data", "port_xmit_packets", "port_rcv_packets",
		"port_xmit_discards", "port_rcv_errors", "symbol_error", "link_error_recovery",
		"link_downed", "port_rcv_remote_physical_errors", "port_rcv_switch_relay_errors",
		"local_link_integrity_errors", "excessive_buffer_overrun_errors",
		"VL15_dropped", "port_xmit_constraint_errors", "port_rcv_constraint_errors",
	}
	for _, c := range zeroCounters {
		if err := writeFile(root, filepath.Join(portDir, "counters", c), "0\n"); err != nil {
			return err
		}
	}

	// libibumad device-name registration.
	madDir := fmt.Sprintf("sys/class/infiniband_mad/umad%d", idx)
	if err := mkdirAll(root, madDir); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(madDir, "ibdev"), caName+"\n"); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(madDir, "port"), "1\n"); err != nil {
		return err
	}
	issmDir := fmt.Sprintf("sys/class/infiniband_mad/issm%d", idx)
	if err := mkdirAll(root, issmDir); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(issmDir, "ibdev"), caName+"\n"); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(issmDir, "port"), "1\n"); err != nil {
		return err
	}

	// libibverbs device-name registration.
	verbsDir := fmt.Sprintf("sys/class/infiniband_verbs/uverbs%d", idx)
	if err := mkdirAll(root, verbsDir); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(verbsDir, "ibdev"), caName+"\n"); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(verbsDir, "abi_version"), "1\n"); err != nil {
		return err
	}
	// libibverbs setup_sysfs_uverbs() reads major:minor from dev.
	if err := writeFile(root, filepath.Join(verbsDir, "dev"), fmt.Sprintf("231:%d\n", idx)); err != nil {
		return err
	}

	// libibverbs matches each sysfs device against provider modalias tables
	// (libibverbs/init.c match_modalias -> fnmatch). The kernel modalias
	// grammar is "pci:v<8H>d<8H>sv<8H>sd<8H>bc<2H>sc<2H>i<2H>" with
	// upper-case hex, zero-padded fields — otherwise libmlx5's match
	// pattern "pci:v000015B3d*sv*sd*bc*sc*i*" never claims the device and
	// ibv_devinfo reports "0 HCAs found". Use the ConnectX-5 (0x1017) PCI
	// IDs (vendor 0x15B3 Mellanox, subsystem 0x15B3:0x0008, class
	// 0x028000 = Infiniband controller).
	if err := mkdirAll(root, filepath.Join(caDir, "device")); err != nil {
		return err
	}
	const modalias = "pci:v000015B3d00001017sv000015B3sd00000008bc02sc00i00\n"
	if err := writeFile(root, filepath.Join(caDir, "device/modalias"), modalias); err != nil {
		return err
	}
	if err := mkdirAll(root, filepath.Join(portDir, "gid_attrs/types")); err != nil {
		return err
	}
	if err := mkdirAll(root, filepath.Join(portDir, "gid_attrs/ndevs")); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(portDir, "gid_attrs/types/0"), "RoCE v2\n"); err != nil {
		return err
	}
	if err := writeFile(root, filepath.Join(portDir, "gid_attrs/ndevs/0"), "1\n"); err != nil {
		return err
	}

	// /dev/infiniband device files. Real char-dev creation requires
	// CAP_MKNOD; regular files are sufficient for sysfs-only consumers
	// (ibstat, ibstatus, iblinkinfo). umad_open_port / ibv_open_device
	// will fail at ioctl time, which is out of scope.
	for _, f := range []string{
		fmt.Sprintf("dev/infiniband/umad%d", idx),
		fmt.Sprintf("dev/infiniband/issm%d", idx),
		fmt.Sprintf("dev/infiniband/uverbs%d", idx),
	} {
		if err := writeDevNodePlaceholder(root, f); err != nil {
			return err
		}
	}
	return nil
}

// nameValue is a (filename, contents) pair used to keep file creation
// order deterministic; map iteration in Go is randomized.
type nameValue struct {
	name  string
	value string
}

// formatPortState turns "ACTIVE" / "INIT" / etc. into the kernel's
// "<num>: <NAME>" sysfs format.
func formatPortState(s string) string {
	switch strings.ToUpper(s) {
	case "DOWN":
		return "1: DOWN\n"
	case "INIT":
		return "2: INIT\n"
	case "ARMED":
		return "3: ARMED\n"
	case "ACTIVE", "":
		return "4: ACTIVE\n"
	case "ACTIVE_DEFER":
		return "5: ACTIVE_DEFER\n"
	default:
		return "4: ACTIVE\n"
	}
}

func formatPhysState(s string) string {
	switch strings.ToUpper(s) {
	case "DISABLED":
		return "3: Disabled\n"
	case "POLLING":
		return "2: Polling\n"
	case "TRAINING":
		return "4: Training\n"
	case "LINKUP", "":
		return "5: LinkUp\n"
	case "LINKERRORRECOVERY":
		return "6: LinkErrorRecovery\n"
	case "PHYTEST":
		return "7: Phy Test\n"
	default:
		return "5: LinkUp\n"
	}
}

// formatRate renders the kernel's "<num> Gb/sec (<width> <speed>)" string
// for a few common InfiniBand speeds.
func formatRate(gbps int) string {
	switch gbps {
	case 100:
		return "100 Gb/sec (4X EDR)"
	case 200:
		return "200 Gb/sec (4X HDR)"
	case 400:
		return "400 Gb/sec (4X NDR)"
	case 800:
		return "800 Gb/sec (4X XDR)"
	default:
		return fmt.Sprintf("%d Gb/sec (4X)", gbps)
	}
}

// normalizeGUIDPrefix strips ':' separators and lowercases the result.
func normalizeGUIDPrefix(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, ":", ""))
}

func isHexString(s string) bool {
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return s != ""
}

const (
	maxGUIDHCAs    = 256
	lidBase        = 0x0100
	lidUnicastHi   = 0xbfff
	maxUnicastLIDs = lidUnicastHi - lidBase + 1
)

// hcaIdentity packs node id and HCA index into the lower 32 bits of the GUID,
// reserving bit 0 as the EUI-64 U/L bit. idx occupies bits 1..8 (up to 256
// HCAs) and the node id occupies bits 9..31 (23 bits). That avoids both the
// low-bit node hash collisions and the mlx5_16 -> mlx5_0 index wrap caused by
// the earlier 16-bit lower-word encoding.
func hcaIdentity(nid uint32, idx int) uint32 {
	return (nid&0x007fffff)<<9 | uint32(idx)<<1
}

// hcaLID derives a per-(node,HCA) LID inside the unicast range. LIDs are only
// 16 bits, so they cannot carry the full GUID identity; use the actual HCA
// count as the node stride to avoid index wrap and maximize available node
// buckets for the configured profile.
func hcaLID(nid uint32, idx, hcaCount int) int {
	if hcaCount <= 0 {
		hcaCount = 1
	}
	buckets := maxUnicastLIDs / hcaCount
	if buckets <= 0 {
		buckets = 1
	}
	return lidBase + int(nid%uint32(buckets))*hcaCount + idx
}

// perHCAGUID renders the colon-separated 8-byte node GUID for HCA index idx.
func perHCAGUID(guidPrefix string, nid uint32, idx int) string {
	lower := hcaIdentity(nid, idx)
	return fmt.Sprintf("%s:%s:%04x:%04x",
		guidPrefix[0:4], guidPrefix[4:8], lower>>16, lower&0xffff)
}

// perHCAPortGUID derives the port GUID by setting the U/L bit (bit 0) on the
// node GUID, matching real Mellanox HCAs where node and port GUIDs differ in
// that bit. Because the identity (node id + HCA index) lives in bits 1..31,
// no port GUID can collide with another HCA's node GUID on the same host.
func perHCAPortGUID(guidPrefix string, nid uint32, idx int) string {
	lower := hcaIdentity(nid, idx) | 1
	return fmt.Sprintf("%s:%s:%04x:%04x",
		guidPrefix[0:4], guidPrefix[4:8], lower>>16, lower&0xffff)
}

func mkdirAll(root, rel string) error {
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", rel, err)
	}
	return nil
}

func writeFile(root, rel, contents string) error {
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(rel), err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// writeDevNodePlaceholder stages an empty placeholder regular file for a
// /dev/infiniband node, but leaves an existing *special* file untouched. A
// privileged deployment (deployments/nvml-mock/scripts/setup.sh) upgrades
// these placeholders to real character devices via mknod; the MOCK_IB=full
// daemon then re-renders idempotently before serving. Opening a mock char
// device (bogus major/minor, no backing driver) for writing fails with ENXIO
// ("no such device or address"), which previously crashed that re-render and
// left the ping socket unbound. So only (re)write a placeholder when the path
// is absent or already a regular file.
func writeDevNodePlaceholder(root, rel string) error {
	full := filepath.Join(root, rel)
	if fi, err := os.Lstat(full); err == nil && !fi.Mode().IsRegular() {
		return nil
	}
	return writeFile(root, rel, "")
}
