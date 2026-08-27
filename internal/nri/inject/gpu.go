// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"log/slog"
	"os"
	"strings"
)

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
		slog.Warn("device injection requested but the device plugin already served this container; leaving its allocation intact")

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
			slog.Warn("cdi device injection requested but no spec is staged; falling back to raw device nodes",
				"spec", cfg.CDISpecHostPath)
		}
		attachRawGPUNodes(cfg, adjustment)
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
		slog.Warn("device injection requested but the device tree is unavailable; injecting overlay only",
			"path", cfg.DeviceHostPath, "err", err)

	case len(devices) == 0:
		// The directory is readable and holds nothing we recognise, so
		// os.ReadDir reports success and the case above never fires. Injecting
		// silently would hand the container an overlay with no device nodes,
		// and the engine derives its visible-GPU set from which /dev/nvidiaN
		// are present — so the pod reports zero GPUs as though that were the
		// configured state. Still fail open, but say so.
		slog.Warn("device injection requested but the device tree holds no device nodes; "+
			"injecting overlay only (has the node agent staged this node?)",
			"path", cfg.DeviceHostPath)

	default:
		adjustment.Devices = append(adjustment.Devices, devices...)
	}
}

// alreadyHasGPUDevices reports whether the container arrived carrying GPU
// devices that something else put there — in practice the NVIDIA device plugin,
// whose Allocate response the kubelet applies before the runtime asks this
// plugin to adjust anything. It recognises both delivery mechanisms that plugin
// supports: raw device nodes (--pass-device-specs) and CDI device references
// (--device-list-strategy=cdi-*).
func alreadyHasGPUDevices(container Container) bool {
	for _, device := range container.Devices {
		if strings.HasPrefix(device.Path, "/dev/nvidia") {
			return true
		}
	}
	for _, device := range container.CDIDevices {
		if strings.HasPrefix(device, "nvidia.com/") {
			return true
		}
	}
	return false
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
