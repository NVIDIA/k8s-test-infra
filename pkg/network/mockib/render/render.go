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
	if len(guidPrefix) != 12 {
		return fmt.Errorf("guid_prefix must be 12 hex digits after stripping ':' (got %q -> %q)", ib.GUIDPrefix, guidPrefix)
	}

	hcaCount := ib.HCACountOverride
	if hcaCount <= 0 {
		hcaCount = o.GPUCount * ib.HCAsPerGPU
	}
	if hcaCount <= 0 {
		return fmt.Errorf("infiniband: hca_count is 0 (gpu_count=%d, hcas_per_gpu=%d)", o.GPUCount, ib.HCAsPerGPU)
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
		if err := renderHCA(root, ib, guidPrefix, i, o.NodeName); err != nil {
			return fmt.Errorf("rendering mlx5_%d: %w", i, err)
		}
	}
	return nil
}

func renderHCA(root string, ib config.Infiniband, guidPrefix string, idx int, nodeName string) error {
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
		{"lid", fmt.Sprintf("0x%04x\n", 0x0100+int(nid&0xff)*16+idx)},
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

	portLower := (nid << 8) | uint16(idx+1)
	gid := fmt.Sprintf("fe80:0000:0000:0000:%s:%s:%s:%04x",
		guidPrefix[0:4], guidPrefix[4:8], guidPrefix[8:12], portLower)
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

	// /dev/infiniband device files. Real char-dev creation requires
	// CAP_MKNOD; regular files are sufficient for sysfs-only consumers
	// (ibstat, ibstatus, iblinkinfo). umad_open_port / ibv_open_device
	// will fail at ioctl time, which is out of scope.
	for _, f := range []string{
		fmt.Sprintf("dev/infiniband/umad%d", idx),
		fmt.Sprintf("dev/infiniband/issm%d", idx),
		fmt.Sprintf("dev/infiniband/uverbs%d", idx),
	} {
		if err := writeFile(root, f, ""); err != nil {
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

// perHCAGUID renders the colon-separated 8-byte node GUID for HCA index idx.
// The lower 16 bits encode (nid<<8)|idx so GUIDs are unique per node and HCA.
func perHCAGUID(guidPrefix string, nid uint16, idx int) string {
	lower := (nid << 8) | uint16(idx)
	return fmt.Sprintf("%s:%s:%s:%04x",
		guidPrefix[0:4], guidPrefix[4:8], guidPrefix[8:12], lower)
}

// perHCAPortGUID derives a port GUID by flipping a single byte (matches
// real Mellanox HCAs where node and port GUIDs differ in the U/L bit).
func perHCAPortGUID(guidPrefix string, nid uint16, idx int) string {
	lower := (nid << 8) | uint16(idx+1)
	return fmt.Sprintf("%s:%s:%s:%04x",
		guidPrefix[0:4], guidPrefix[4:8], guidPrefix[8:12], lower)
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
