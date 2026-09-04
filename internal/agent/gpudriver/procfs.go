// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
)

// procFSRel is the staged procfs tree. The NVIDIA container runtime bind-mounts
// this directory into containers, so it is one of the few driver interfaces a
// workload can still read after isolation.
const procFSRel = "driver/proc/driver/nvidia"

// defaultImexChannelCount is the nvidia module's own ImexChannelCount default,
// reported when no IMEX surface is configured.
const defaultImexChannelCount = 2048

// firstGPUIRQ is where the per-GPU IRQ numbers in the information files start.
const firstGPUIRQ = 24

// writeProcFS provides the procfs entries that consumers read without going
// through NVML: the driver version banner, the module parameters
// nvidia-modprobe parses, and the per-GPU identity directories that map a PCI
// address to a device minor.
func writeProcFS(ctx context.Context, h *host.Host, state *agent.State) error {
	procDir := filepath.Join(h.Root, procFSRel)
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	version := fmt.Sprintf(
		"NVRM version: NVIDIA UNIX x86_64 Kernel Module  %s  Thu Feb 20 23:41:34 UTC 2026\n"+
			"GCC version:  gcc version 12.2.0 (Debian 12.2.0-14)\n",
		state.Software.DriverVersion,
	)
	if err := fsutil.Write(filepath.Join(procDir, "version"), []byte(version), 0o644); err != nil {
		return err
	}
	if err := fsutil.Write(filepath.Join(procDir, "params"), []byte(renderParams(state)), 0o644); err != nil {
		return err
	}

	return writeGPUDirs(ctx, procDir, state)
}

// writeGPUDirs writes gpus/<BDF>/information, one directory per device.
//
// This is the GPU enumeration path that bypasses NVML: NVIDIA's MIG user guide
// names it as the alternative to nvmlDeviceGetMinorNumber for resolving which
// /dev/nvidiaN belongs to a PCI address, and it is the one that still works
// inside a container. Without it a consumer taking that path sees no GPUs at
// all, however complete the NVML shim is.
func writeGPUDirs(ctx context.Context, procDir string, state *agent.State) error {
	gpusDir := filepath.Join(procDir, "gpus")
	if err := os.MkdirAll(gpusDir, 0o755); err != nil {
		return err
	}

	// Addresses come from the same reconciliation the PCI sysfs renderer reads,
	// for two reasons. The surfaces cannot then disagree about which GPUs exist
	// — a consumer resolving a GPU in one and not the other is the failure both
	// paths exist to fix. And a bus_id is authored by hand in gpu.customConfig
	// and becomes a path component below, so it has to be an address before it
	// is joined onto anything.
	served := servedBDFs(state)
	wanted := make(map[string]bool, len(served))

	for _, d := range state.Devices {
		if err := ctx.Err(); err != nil {
			return err
		}

		// A device the topology leaves out keeps its NVML identity and simply
		// gets no directory; failing the whole staging run over it would take
		// the working GPUs with it.
		bdf := strings.ToLower(d.PCIBusID)
		if !served[bdf] {
			continue
		}

		wanted[bdf] = true

		p := filepath.Join(gpusDir, bdf, "information")
		if err := fsutil.Write(p, []byte(renderInformation(d)), 0o644); err != nil {
			return fmt.Errorf("gpu %s: %w", bdf, err)
		}
	}

	return pruneGPUDirs(gpusDir, wanted)
}

// servedBDFs is the set of PCI addresses the rendered sysfs tree carries,
// already validated and lowercased by the reconciliation.
func servedBDFs(state *agent.State) map[string]bool {
	topology := state.PCITopology()

	served := make(map[string]bool, len(state.Devices))
	for _, rc := range topology {
		for _, bdf := range rc.DeviceBDFs {
			served[bdf] = true
		}
	}

	return served
}

// pruneGPUDirs removes directories left by a larger previous device set.
// Staging is additive, so a shrunk GPU count would otherwise keep advertising
// a PCI address that NVML no longer reports.
func pruneGPUDirs(gpusDir string, wanted map[string]bool) error {
	entries, err := os.ReadDir(gpusDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", gpusDir, err)
	}

	var errs []error

	for _, e := range entries {
		if wanted[e.Name()] {
			continue
		}

		p := filepath.Join(gpusDir, e.Name())
		if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
		}
	}

	return errors.Join(errs...)
}

// renderInformation builds one gpus/<BDF>/information file. Field names and the
// tab-after-colon layout come from the real driver, which consumers grep by
// name — "Device Minor" above all.
func renderInformation(d agent.DeviceSpec) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Model: \t\t %s\n", d.Name)
	// The IRQ is not reachable from the profile and no consumer reads it; it is
	// present so the file has the shape callers expect when they dump it.
	fmt.Fprintf(&b, "IRQ:   \t\t %d\n", firstGPUIRQ+d.Index)

	// Only reported when the profile named one. Synthesising a UUID here would
	// contradict NVML, which falls back to its own base value.
	if d.UUID != "" {
		fmt.Fprintf(&b, "GPU UUID: \t %s\n", d.UUID)
	}

	fmt.Fprintf(&b, "Video BIOS: \t %s\n", d.VBIOSVersion)
	b.WriteString("Bus Type: \t PCIe\n")
	b.WriteString("DMA Size: \t 47 bits\n")
	b.WriteString("DMA Mask: \t 0x7fffffffffff\n")
	fmt.Fprintf(&b, "Bus Location: \t %s\n", strings.ToLower(d.PCIBusID))
	fmt.Fprintf(&b, "Device Minor: \t %d\n", d.MinorNumber)
	b.WriteString("Blacklisted:\t No\n")
	fmt.Fprintf(&b, "Architecture: \t %d.%d\n", d.ComputeCapMajor, d.ComputeCapMinor)
	fmt.Fprintf(&b, "Memory: \t %d MiB\n", d.MemoryTotalBytes/(1<<20))
	b.WriteString("GPU Excluded:\t No\n")

	return b.String()
}

// renderParams builds /proc/driver/nvidia/params. Two properties of
// nvidia-modprobe's parse loop over this file constrain it, and getting either
// wrong leaves the file silently inert
// (modprobe-utils/nvidia-modprobe-utils.c):
//
//	while (fscanf(fp, "%31[^:]: %u\n", name, &value) == 2)
//
// Keys are matched unprefixed. NVreg_ is the modprobe parameter name (options
// nvidia NVreg_DeviceFileMode=0666); procfs reports the resolved value under
// the bare name.
//
// The scan then ends at the first line it cannot consume whole, and every key
// below that line is unreachable. Two things end it: a value that is not a bare
// unsigned integer, and a name past the 31-char field width — which
// InitializeSystemMemoryAllocations, at 33 characters, exceeds. The real
// driver's ordering accounts for both, keeping the four keys nvidia-modprobe
// consumes in the first six lines and grouping the quoted string params at the
// very end. The order is load-bearing, not cosmetic.
func renderParams(state *agent.State) string {
	imexChannels := state.IMEX.ChannelCount
	if imexChannels <= 0 {
		imexChannels = defaultImexChannelCount
	}

	modify := 0
	if state.DriverParams.ModifyDeviceFiles {
		modify = 1
	}

	var b strings.Builder
	for _, p := range []struct {
		key   string
		value int
	}{
		{"ResmanDebugLevel", -1},
		{"RmLogonRC", 1},
		{"ModifyDeviceFiles", modify},
		{"DeviceFileUID", state.DriverParams.DeviceFileUID},
		{"DeviceFileGID", state.DriverParams.DeviceFileGID},
		// Decimal, as the driver reports it: 438 is 0666.
		{"DeviceFileMode", state.DriverParams.DeviceFileMode},
		{"InitializeSystemMemoryAllocations", 1},
		{"UsePageAttributeTable", -1},
		{"EnableMSI", 1},
		{"NvLinkDisable", 0},
		{"PreserveVideoMemoryAllocations", 0},
		{"EnableResizableBar", 0},
		{"EnableGpuFirmware", 18},
		{"ImexChannelCount", imexChannels},
	} {
		// The driver prints these as unsigned, so the module's -1 sentinels
		// surface as 4294967295 rather than a negative value %u cannot read.
		fmt.Fprintf(&b, "%s: %d\n", p.key, uint32(p.value)) //nolint:gosec // sentinels are deliberately wrapped
	}

	// Must stay last: the empty value ends nvidia-modprobe's scan.
	b.WriteString("RegistryDwords: \"\"\n")

	return b.String()
}
