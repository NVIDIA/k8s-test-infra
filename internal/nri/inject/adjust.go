// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject decides what the nvml-mock overlay adds to a container. It is
// the runtime-neutral half of the NRI plugin: no containerd types cross this
// boundary, so the decision is exercisable as a plain table test.
//
// The generic overlay fails open: a surface the node agent has not staged yet
// degrades the injection instead of failing container creation, because
// nothing orders the plugin's container after the agent's. A specialized
// workload states its own policy.
package inject

import (
	"path/filepath"

	"go.uber.org/zap"
)

// Adjust returns what to add to a container, or ok=false when the container
// should be left exactly as authored. The NRI CreateContainer hook calls it for
// every container; it routes the container to the specialized workload that
// recognizes it, or else to the generic overlay. An error comes only from a
// specialized workload and fails the container's creation, so kubelet retries.
func Adjust(cfg Config, container Container) (Adjustment, bool, error) {
	cfg = withDefaults(cfg)
	if reason, skipped := skip(cfg, container); skipped {
		zap.L().Debug("skipping container injection", zap.String("namespace", container.Namespace), zap.String("reason", reason))
		return Adjustment{}, false, nil
	}
	for _, workload := range specializedWorkloads {
		if workload.matches(container) {
			zap.L().Debug("adjusting specialized workload",
				zap.String("namespace", container.Namespace), zap.String("workload", workload.name))
			return workload.adjust(cfg, container)
		}
	}
	return adjustWorkload(cfg, container), true, nil
}

// specializedWorkload replaces the generic overlay for a container that gets
// its driver footprint from elsewhere, such as DRA's CDI edits.
type specializedWorkload struct {
	name    string
	matches func(Container) bool
	// adjust returns ok=false to leave the container unmodified, or an error
	// to fail its creation.
	adjust func(Config, Container) (Adjustment, bool, error)
}

var specializedWorkloads = []specializedWorkload{
	{name: computeDomainContainerName, matches: computeDomainDaemon, adjust: adjustComputeDomain},
}

// adjustWorkload composes the generic overlay. The steps run in a fixed order,
// each contributing to the same adjustment, and none of them can fail.
func adjustWorkload(cfg Config, container Container) Adjustment {
	var adjustment Adjustment
	mountOverlay(cfg, &adjustment)
	setEnvironment(cfg, container, &adjustment)
	attachGPUs(cfg, container, &adjustment)
	attachIMEXChannels(cfg, container, &adjustment)

	zap.L().Debug("injecting container",
		zap.String("namespace", container.Namespace),
		zap.Int("mounts", len(adjustment.Mounts)),
		zap.Int("devices", len(adjustment.Devices)))
	return adjustment
}

// skip reports whether the container must be left exactly as authored, and why.
//
// The mount check is what makes re-adjustment safe: a container that already
// carries the overlay at its destination has been through here before, and
// injecting a second time would stack duplicate LD_PRELOAD entries.
func skip(cfg Config, container Container) (reason string, ok bool) {
	if container.annotated(cfg.OptOutAnnotation, "false") {
		return "opt-out annotation", true
	}
	for _, namespace := range cfg.ExcludedNamespaces {
		if container.Namespace == namespace {
			return "excluded namespace", true
		}
	}
	for _, mount := range container.Mounts {
		if mount.Destination == cfg.ContainerOverlayPath {
			return "overlay already mounted", true
		}
	}
	return "", false
}

// mountOverlay binds the staged mock driver tree into the container, read-only
// except for the config directory.
//
// This is the one step with no opt-in gate: the shims, the mock NVML config and
// the IB sysfs tree all live under this path, so every later step describes
// something reachable only through it.
//
// The config directory is writable because the container writes back through
// it: the mock serves `nvidia-smi --gpu-reset` by clearing the device's bucket
// from overrides.yaml, under the same flock every other writer takes. With the
// whole overlay read-only that write failed with EROFS, so a reset reported
// "GPU Reset couldn't run" on exactly the GPUs that had state to clear, while
// healthy ones reported success by skipping the write. Layering a narrower
// writable bind over the read-only tree keeps the mock library and nvidia-smi
// below it immutable; the order matters, since the overlay would cover this
// mount if it came second.
func mountOverlay(cfg Config, adjustment *Adjustment) {
	mountReadOnlyOverlay(cfg, adjustment)
	adjustment.Mounts = append(adjustment.Mounts,
		Mount{
			Source:      filepath.Join(cfg.HostOverlayPath, configRelPath),
			Destination: filepath.Join(cfg.ContainerOverlayPath, configRelPath),
			Type:        "bind",
			Options:     []string{"rbind", "rw", "nosuid", "nodev"},
		},
	)
}

func mountReadOnlyOverlay(cfg Config, adjustment *Adjustment) {
	adjustment.Mounts = append(adjustment.Mounts, Mount{
		Source:      cfg.HostOverlayPath,
		Destination: cfg.ContainerOverlayPath,
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	})
}
