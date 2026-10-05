// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// DeviceInjectionMode selects how unallocated management containers receive
// mock GPUs. Existing scheduler-backed allocations are never widened.
type DeviceInjectionMode string

const (
	// DeviceInjectionModeRaw stages the mock /dev/nvidiaN nodes directly in the
	// adjustment. It is the default: MEP-0002 requires the raw path to stay
	// reachable, and it is the only mode that works on a runtime whose CDI
	// support is off or absent.
	DeviceInjectionModeRaw DeviceInjectionMode = "raw"
	// DeviceInjectionModeCDI hands the runtime a CDI device reference and lets
	// it resolve the device nodes from the spec the cdi simulator writes.
	// containerd 2.x enables CDI by default (enable_cdi = true, spec dirs
	// /etc/cdi and /var/run/cdi), so this needs no container toolkit on the node.
	DeviceInjectionModeCDI DeviceInjectionMode = "cdi"
)

// ParseDeviceInjectionMode rejects an unknown mode rather than coercing it. A
// typo that silently resolved to raw would look exactly like a working CDI
// deployment, and the difference is only visible in the OCI spec of an
// already-running pod.
func ParseDeviceInjectionMode(s string) (DeviceInjectionMode, error) {
	switch mode := DeviceInjectionMode(strings.ToLower(strings.TrimSpace(s))); mode {
	case DeviceInjectionModeRaw, DeviceInjectionModeCDI:
		return mode, nil
	case "":
		return DeviceInjectionModeRaw, nil
	default:
		return "", fmt.Errorf("invalid device injection mode %q: expected %q or %q",
			s, DeviceInjectionModeRaw, DeviceInjectionModeCDI)
	}
}

// attachGPUs adds all mock GPUs only for unallocated management containers.
// An allocation already carries its exact device set in the incoming spec.
func attachGPUs(cfg Config, adjustment *Adjustment) {
	switch {
	case cfg.DeviceInjectionMode == DeviceInjectionModeCDI && cdiSpecStaged(cfg):
		// The runtime resolves the device nodes from the staged spec. Nothing is
		// added to adjustment.Devices: the CDI reference and the raw nodes
		// describe the same GPUs, and delivering both would widen the container
		// and defeat the engine's detectVisibleDevices filter.
		adjustment.CDIDevices = []string{cfg.CDIDeviceName}

	default:
		if cfg.DeviceInjectionMode == DeviceInjectionModeCDI {
			// containerd fails container creation outright on an unresolvable
			// CDI device, so an unstaged spec falls back to raw nodes.
			zap.L().Warn("cdi device injection requested but no spec is staged; falling back to raw device nodes",
				zap.String("spec", cfg.CDISpecHostPath))
		}
		attachRawGPUNodes(cfg, adjustment)
	}
}

// attachRawGPUNodes stages the mock /dev/nvidia* nodes directly.
//
// Both failure arms fail open. The device tree is staged by the node agent and
// nothing orders this plugin's container after it, so a fresh or unreadable
// node degrades to overlay-only injection rather than blocking the whole pod.
func attachRawGPUNodes(cfg Config, adjustment *Adjustment) {
	devices, err := discoverDevices(cfg.DeviceHostPath)
	switch {
	case err != nil:
		zap.L().Warn("device injection requested but the device tree is unavailable; injecting overlay only",
			zap.String("path", cfg.DeviceHostPath), zap.Error(err))

	case len(devices) == 0:
		// The directory is readable and holds nothing we recognise, so
		// os.ReadDir reports success and the case above never fires. Injecting
		// silently would hand the container an overlay with no device nodes,
		// and the engine derives its visible-GPU set from which /dev/nvidiaN
		// are present — so the pod reports zero GPUs as though that were the
		// configured state. Still fail open, but say so.
		zap.L().Warn("device injection requested but the device tree holds no device nodes; "+
			"injecting overlay only (has the node agent staged this node?)",
			zap.String("path", cfg.DeviceHostPath))

	default:
		adjustment.Devices = append(adjustment.Devices, devices...)
	}
}

// hasGPUAllocation accepts only explicit NVIDIA GPU CDI identities or a
// numbered character device with its own exact cgroup allow. Inherited host
// devices and privileged wildcard rules do not prove scheduler allocation.
//
// The CDI identities are the ones the allocators themselves name: the device
// plugin's cdi-cri strategy uses its fixed k8s.device-plugin.nvidia.com vendor
// (its gdrcopy and mofed classes ride along with any allocation and identify
// no GPU), and the DRA driver names one device per claim.
func hasGPUAllocation(container Container) bool {
	for _, name := range container.CDIDevices {
		if validCDIIdentity(name, "k8s.device-plugin.nvidia.com/gpu=") || validCDIIdentity(name, "k8s.gpu.nvidia.com/claim=") {
			return true
		}
	}
	return hasRawGPUAllocation(container)
}

func hasRawGPUAllocation(container Container) bool {
	for _, device := range container.IncomingDevices {
		if !numberedGPUPath(device.Path) || device.Type != "c" {
			continue
		}
		if hasExactDeviceGrant(device, container.DeviceRules) {
			return true
		}
	}
	return false
}

func hasExactDeviceGrant(device RuntimeDevice, rules []DeviceRule) bool {
	for _, rule := range rules {
		if rule.Allow && rule.Type == device.Type && rule.Major != nil && rule.Minor != nil &&
			*rule.Major == device.Major && *rule.Minor == device.Minor && strings.ContainsAny(rule.Access, "rw") {
			return true
		}
	}
	return false
}

func validCDIIdentity(name, prefix string) bool {
	value, ok := strings.CutPrefix(name, prefix)
	return ok && value != "" && !strings.ContainsAny(value, "/= ")
}

func numberedGPUPath(path string) bool {
	index, ok := strings.CutPrefix(path, "/dev/nvidia")
	if !ok || index == "" {
		return false
	}
	n, err := strconv.ParseUint(index, 10, 32)
	return err == nil && strconv.FormatUint(n, 10) == index
}

// cdiSpecStaged reports whether the CDI spec backing cfg.CDIDeviceName is
// present on the node. The stat runs per container, like topologyInjectable, so
// the plugin tolerates the agent writing the spec after the plugin starts.
func cdiSpecStaged(cfg Config) bool {
	if cfg.CDISpecHostPath == "" {
		return false
	}
	_, err := os.Stat(cfg.CDISpecHostPath)
	return err == nil
}
