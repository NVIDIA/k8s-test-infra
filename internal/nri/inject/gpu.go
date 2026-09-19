// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

// DeviceInjectionMode selects the mechanism the device opt-in uses to deliver
// mock GPUs. It changes the mechanism only: whether a container is served at
// all is decided before this is consulted, so neither mode can inject into a
// container the device plugin already served (MEP-0002).
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

// attachGPUs delivers the mock GPU tree to a container that opted in.
func attachGPUs(cfg Config, container Container, adjustment *Adjustment) {
	if !container.annotated(cfg.DeviceAnnotation, "true") {
		return
	}

	switch {
	case alreadyHasGPUDevices(container):
		// MEP-0002: the device plugin allocated a specific GPU and the kubelet
		// already applied it. Adding the whole device tree on top would widen
		// the container past its allocation, and would defeat the mock engine's
		// visibility filter, which derives the visible GPU set from which
		// /dev/nvidiaN nodes are present.
		zap.L().Warn("device injection requested but the device plugin already served this container; leaving its allocation intact")

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

// attachAllocatedGPUControls completes the raw device-plugin path with the
// node-wide control devices a real NVIDIA runtime normally supplies. It never
// adds another /dev/nvidiaN, so the allocated GPU subset remains authoritative.
// CDI allocations already describe their complete edits and are left alone.
func attachAllocatedGPUControls(cfg Config, container Container, adjustment *Adjustment) {
	if !hasRawAllocatedGPU(container) || hasNVIDIACDIAllocation(container) {
		return
	}

	devices, err := discoverDevices(cfg.DeviceHostPath)
	if err != nil {
		zap.L().Warn("gpu allocation detected but the control device tree is unavailable; injecting overlay only",
			zap.String("path", cfg.DeviceHostPath), zap.Error(err))
		return
	}

	existing := make(map[string]struct{}, len(container.Devices)+len(adjustment.Devices))
	for _, device := range container.Devices {
		existing[device.Path] = struct{}{}
	}
	for _, device := range adjustment.Devices {
		existing[device.Path] = struct{}{}
	}
	for _, device := range devices {
		if !isGPUControlPath(device.Path) {
			continue
		}
		if _, ok := existing[device.Path]; ok {
			continue
		}
		adjustment.Devices = append(adjustment.Devices, device)
	}
}

// attachRawGPUNodes stages the mock /dev/nvidia* nodes directly.
//
// Both failure arms fail open. The device tree is staged by the node agent and
// nothing orders this plugin's DaemonSet after it, so a fresh or unreadable
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

// alreadyHasGPUDevices reports whether the container arrived carrying GPU
// allocation evidence from the device plugin or DRA driver. Control nodes alone
// are not allocation evidence. Both raw device nodes and CDI references are
// recognised.
func alreadyHasGPUDevices(container Container) bool {
	return hasAllocatedGPU(container)
}

func hasAllocatedGPU(container Container) bool {
	return hasRawAllocatedGPU(container) || hasNVIDIACDIAllocation(container)
}

func hasRawAllocatedGPU(container Container) bool {
	for _, device := range container.Devices {
		if isPerGPUDevicePath(device.Path) {
			return true
		}
	}
	return false
}

func hasNVIDIACDIAllocation(container Container) bool {
	for _, device := range container.CDIDevices {
		if strings.HasPrefix(device, "nvidia.com/") ||
			strings.HasPrefix(device, "k8s.gpu.nvidia.com/") {
			return true
		}
	}
	return false
}

func isPerGPUDevicePath(path string) bool {
	index := strings.TrimPrefix(path, "/dev/nvidia")
	if index == path || index == "" {
		return false
	}
	for _, r := range index {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isGPUControlPath(path string) bool {
	switch path {
	case "/dev/nvidiactl", "/dev/nvidia-uvm", "/dev/nvidia-uvm-tools":
		return true
	default:
		return false
	}
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
