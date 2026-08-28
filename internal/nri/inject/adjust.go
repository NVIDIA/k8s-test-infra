// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject decides what the nvml-mock overlay adds to a container. It is
// the runtime-neutral half of the NRI plugin: no containerd types cross this
// boundary, so the decision is exercisable as a plain table test.
//
// Every step fails open. A surface the node agent has not staged yet degrades
// the injection instead of failing container creation, because nothing orders
// the plugin's DaemonSet after the agent's.
package inject

// Adjust returns what to add to a container, or ok=false when the container
// should be left exactly as authored.
//
// The steps run in a fixed order, each contributing to the same adjustment.
// None of them can fail, which is why there is no error to return.
func Adjust(cfg Config, container Container) (Adjustment, bool) {
	cfg = withDefaults(cfg)
	if skip(cfg, container) {
		return Adjustment{}, false
	}

	var adjustment Adjustment
	mountOverlay(cfg, &adjustment)
	setEnvironment(cfg, container, &adjustment)
	attachGPUs(cfg, container, &adjustment)
	attachIMEXChannels(cfg, container, &adjustment)

	return adjustment, true
}

// skip reports whether the container must be left exactly as authored.
//
// The mount check is what makes re-adjustment safe: a container that already
// carries the overlay at its destination has been through here before, and
// injecting a second time would stack duplicate LD_PRELOAD entries.
func skip(cfg Config, container Container) bool {
	if container.annotated(cfg.OptOutAnnotation, "false") {
		return true
	}
	for _, namespace := range cfg.ExcludedNamespaces {
		if container.Namespace == namespace {
			return true
		}
	}
	for _, mount := range container.Mounts {
		if mount.Destination == cfg.ContainerOverlayPath {
			return true
		}
	}
	return false
}

// mountOverlay binds the staged mock driver tree into the container.
//
// This is the one step with no opt-in gate: the shims, the mock NVML config and
// the IB sysfs tree all live under this path, so every later step describes
// something reachable only through it.
func mountOverlay(cfg Config, adjustment *Adjustment) {
	adjustment.Mounts = append(adjustment.Mounts, Mount{
		Source:      cfg.HostOverlayPath,
		Destination: cfg.ContainerOverlayPath,
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
}
