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

// gpuIRQ is the interrupt every GPU reports in its information file, as the
// captured node reports it — one shared MSI-X vector rather than a line per
// device.
const gpuIRQ = 254

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
	// The open kernel module's banner, since the params key set below is the
	// open module's too (OpenRmEnableUnsupportedGpus has no proprietary
	// counterpart) and a node cannot be running both.
	version := fmt.Sprintf(
		"NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  %s  Release Build  (mokka@nvml-mock)  Thu Feb 20 23:41:34 UTC 2026\n"+
			"GCC version:  gcc version 12.2.0 (Debian 12.2.0-14)\n",
		state.Software.DriverVersion,
	)
	if err := fsutil.Write(filepath.Join(procDir, "version"), []byte(version), 0o644); err != nil {
		return err
	}
	params, err := renderParams(state)
	if err != nil {
		return err
	}
	if err := fsutil.Write(filepath.Join(procDir, "params"), []byte(params), 0o644); err != nil {
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
		if err := fsutil.Write(p, []byte(renderInformation(d, state.Software.DriverVersion)), 0o644); err != nil {
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

// renderInformation builds one gpus/<BDF>/information file. The field set, the
// order and the tab-after-colon layout are those of the real driver, captured
// from an 8-GPU H100 node running the 580.105.08 open kernel module, because
// consumers grep this file by field name — "Device Minor" above all.
func renderInformation(d agent.DeviceSpec, driverVersion string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Model: \t\t %s\n", d.Name)
	// Every GPU on the captured node reports the same IRQ, so this is a
	// constant rather than a per-device number. No consumer reads it; it is
	// here so the file has the shape callers expect when they dump it.
	fmt.Fprintf(&b, "IRQ:   \t\t %d\n", gpuIRQ)

	// Only reported when the profile named one. Synthesising a UUID here would
	// contradict NVML, which falls back to its own base value.
	if d.UUID != "" {
		fmt.Fprintf(&b, "GPU UUID: \t %s\n", d.UUID)
	}

	fmt.Fprintf(&b, "Video BIOS: \t %s\n", d.VBIOSVersion)
	b.WriteString("Bus Type: \t PCIe\n")
	b.WriteString("DMA Size: \t 52 bits\n")
	b.WriteString("DMA Mask: \t 0xfffffffffffff\n")
	fmt.Fprintf(&b, "Bus Location: \t %s\n", strings.ToLower(d.PCIBusID))
	fmt.Fprintf(&b, "Device Minor: \t %d\n", d.MinorNumber)
	// The GSP firmware version tracks the driver version on real hardware, and
	// the driver prints this row whenever firmware is in use — which
	// EnableGpuFirmware: 18 in params says it is.
	fmt.Fprintf(&b, "GPU Firmware: \t %s\n", driverVersion)
	b.WriteString("GPU Excluded:\t No\n")

	return b.String()
}
