// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/kmod"
)

// writeProcFS provides the procfs entries that libnvidia-ml and
// nvidia-container-toolkit read to discover driver version without dlopen.
func writeProcFS(ctx context.Context, h *host.Host, state *agent.State) error {
	procDir := h.RootPath(procDriverRelPath)
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
	// The driver prints its registry table as "<Key>: <value>" on the bare key
	// name, in table order, then each string key with its value quoted.
	// NVreg_ is only the module-parameter spelling (modprobe nvidia NVreg_...).
	const params = "ModifyDeviceFiles: 1\n" +
		"DeviceFileUID: 0\n" +
		"DeviceFileGID: 0\n" +
		"DeviceFileMode: 438\n" +
		"EnableMSI: 1\n" +
		"PreserveVideoMemoryAllocations: 0\n" +
		"EnableResizableBar: 0\n" +
		"RegistryDwords: \"\"\n"
	if err := fsutil.Write(filepath.Join(procDir, "params"), []byte(params), 0o644); err != nil {
		return err
	}
	if err := writeGPUInformation(filepath.Join(procDir, "gpus"), state.Devices); err != nil {
		return err
	}
	// libmockfs serves /proc/driver/nvidia from here, beside /proc/modules; the
	// driver tree keeps the canonical copy for /run/nvidia/driver consumers.
	return fsutil.Symlink(filepath.Join("..", "..", procDriverRelPath), h.RootPath(procDriverLinkRelPath))
}

const (
	procDriverRelPath     = "driver/proc/driver/nvidia"
	procDriverLinkRelPath = "proc/driver/nvidia"
)

// writeGPUInformation writes the per-GPU directories the driver keeps under
// /proc/driver/nvidia/gpus, named by lowercase BDF, and prunes those of GPUs
// that are gone. Slurm's gres AutoDetect=nvidia enumerates GPUs from them.
// Only the lines backed by the device spec are written: a consumer that needs
// the IRQ or VBIOS gets nothing rather than a value no profile declares.
func writeGPUInformation(dir string, devices []agent.DeviceSpec) error {
	keep := make(map[string]bool, len(devices))
	for _, d := range devices {
		bdf := strings.ToLower(d.PCIBusID)
		if !agent.ValidBDF(bdf) {
			continue
		}
		keep[bdf] = true

		info := fmt.Sprintf("Model: \t\t %s\n"+
			"GPU UUID: \t %s\n"+
			"Bus Type: \t PCIe\n"+
			"Bus Location: \t %s\n"+
			"Device Minor: \t %d\n"+
			"GPU Excluded:\t No\n",
			d.Name, d.UUID, bdf, d.MinorNumber)
		if err := os.MkdirAll(filepath.Join(dir, bdf), 0o755); err != nil {
			return err
		}
		if err := fsutil.Write(filepath.Join(dir, bdf, "information"), []byte(info), 0o444); err != nil {
			return err
		}
	}

	return fsutil.PruneDir(dir, func(name string) bool { return keep[name] })
}

const kernelSysModuleDir = "module"

// kernelProcModules is the agent's own /proc/modules, not h.ProcPath("modules"):
// the kernel does not namespace the module list, so the DaemonSet mounts no
// /host/proc. A package var, as pcibus and ib hold theirs, so a test can retarget it.
var kernelProcModules = "/proc/modules"

func writeKernelModules(ctx context.Context, h *host.Host, state *agent.State) error {
	mods := kmod.Modules(state.Software.DriverVersion, state.NodeShape.Network.IBEnabled)

	// Absent and empty both mean a node that loads no module, and a masked /proc
	// entry reads as empty. A read that fails is fatal: this file is the only
	// source of host presence for both surfaces.
	src, err := os.ReadFile(kernelProcModules)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", kernelProcModules, err)
	}
	hostMods := kmod.ParseProcModules(string(src))

	if err := ctx.Err(); err != nil {
		return err
	}

	skipped, err := kmod.Render(kmod.Options{
		Modules:     mods,
		SourceRoot:  h.SysPath(kernelSysModuleDir),
		OverlayRoot: h.Root,
		Host:        hostMods,
	})
	if err != nil {
		return err
	}
	if len(skipped) > 0 {
		slog.Debug("kernel module sources unreadable", "count", len(skipped), "paths", skipped)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	procModules := kmod.ProcModules(hostMods, mods)
	if err := fsutil.Write(h.RootPath(kmod.ProcModulesRelPath), []byte(procModules), 0o644); err != nil {
		return err
	}

	return fsutil.Write(h.RootPath(kmod.LsmodRelPath), []byte(kmod.LsmodScript), 0o755)
}
