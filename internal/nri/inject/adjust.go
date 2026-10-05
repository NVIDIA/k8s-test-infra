// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject decides what the nvml-mock overlay adds to a container. It is
// the runtime-neutral half of the NRI plugin: no containerd types cross this
// boundary, so the decision is exercisable as a plain table test.
//
// The generic overlay fails open: a missing surface degrades injection instead
// of failing container creation. A specialized workload states its own policy.
// The runtime-facing plugin separately gates adjustments while the node agent
// is staging or unavailable.
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
	sel, reason, skipped := decide(cfg, container)
	if skipped {
		zap.L().Debug("skipping container injection", zap.String("namespace", container.Namespace), zap.String("reason", reason))
		return Adjustment{}, false, nil
	}
	if sel.workload != nil {
		zap.L().Debug("adjusting specialized workload",
			zap.String("namespace", container.Namespace), zap.String("workload", sel.workload.name))
		return sel.workload.adjust(cfg, container)
	}
	return adjustWorkload(cfg, container, sel), true, nil
}

// WaitsForStaging reports whether the container is a specialized workload that
// must not run until the node agent has staged what it needs. While the agent
// is staging or unavailable, the plugin fails such a container's creation so
// kubelet retries, instead of leaving it unmodified.
func WaitsForStaging(cfg Config, container Container) bool {
	cfg = withDefaults(cfg)
	sel, _, skipped := decide(cfg, container)
	return !skipped && sel.workload != nil && sel.workload.waitsForStaging(cfg)
}

// specializedWorkload replaces the generic overlay for a container that gets
// its driver footprint from elsewhere, such as DRA's CDI edits.
type specializedWorkload struct {
	name    string
	matches func(Container) bool
	// adjust returns ok=false to leave the container unmodified, or an error
	// to fail its creation.
	adjust func(Config, Container) (Adjustment, bool, error)
	// waitsForStaging reports whether the workload needs node agent staging
	// before it can run.
	waitsForStaging func(Config) bool
}

var specializedWorkloads = []specializedWorkload{
	{
		name:            computeDomainContainerName,
		matches:         computeDomainDaemon,
		adjust:          adjustComputeDomain,
		waitsForStaging: func(cfg Config) bool { return cfg.ComputeDomainStaging },
	},
}

// adjustWorkload composes the generic overlay. The steps run in a fixed order,
// each contributing to the same adjustment, and none of them can fail.
func adjustWorkload(cfg Config, container Container, sel selection) Adjustment {
	if sel.allocated && container.annotated(cfg.DeviceAnnotation, "true") {
		zap.L().Debug("GPU allocation takes precedence over the devices annotation",
			zap.String("namespace", container.Namespace))
	}

	// GPU and IMEX containers receive the same shared overlay and environment.
	var adjustment Adjustment
	mountOverlay(cfg, &adjustment)
	setEnvironment(cfg, container, &adjustment)
	if sel.management {
		attachGPUs(cfg, &adjustment)
	}
	attachIMEXChannels(cfg, container, &adjustment)

	zap.L().Debug("injecting container",
		zap.String("namespace", container.Namespace),
		zap.Int("mounts", len(adjustment.Mounts)),
		zap.Int("devices", len(adjustment.Devices)))
	return adjustment
}

// Skip reports whether Adjust would leave the container exactly as authored,
// and why. It reads only the container, so the plugin can ask before it
// consults the node agent.
func Skip(cfg Config, container Container) (reason string, skipped bool) {
	_, reason, skipped = decide(withDefaults(cfg), container)
	return reason, skipped
}

// selection records which surfaces a container asked for. It is derived from
// the container alone, never from the node's staged tree.
type selection struct {
	// allocated: the container holds a device-plugin or DRA GPU allocation,
	// which it keeps exactly.
	allocated bool
	// management: the devices annotation without an allocation, which
	// delivers every staged GPU.
	management bool
	imex       bool
	// workload is set for a container a specialized workload recognizes. Such
	// a container is routed before the opt-in checks: the DRA ComputeDomain
	// daemon carries no allocation or annotation at CreateContainer.
	workload *specializedWorkload
}

// decide is the single source of truth shared by Adjust and Skip.
//
// The mount check is what makes re-adjustment safe: a container that already
// carries the overlay at its destination has been through here before, and
// injecting a second time would stack duplicate LD_PRELOAD entries.
func decide(cfg Config, container Container) (sel selection, reason string, skipped bool) {
	if reason, skipped := skip(cfg, container); skipped {
		return selection{}, reason, true
	}
	for i := range specializedWorkloads {
		if specializedWorkloads[i].matches(container) {
			return selection{workload: &specializedWorkloads[i]}, "", false
		}
	}
	allocated := hasGPUAllocation(container)
	sel = selection{
		allocated:  allocated,
		management: container.annotated(cfg.DeviceAnnotation, "true") && !allocated,
		imex:       container.annotated(cfg.ImexChannelAnnotation, "true"),
	}
	if !sel.allocated && !sel.management && !sel.imex {
		return sel, "no GPU allocation and no opt-in annotation", true
	}
	if hasOverlay(cfg, container) {
		return sel, "overlay already mounted", true
	}
	return sel, "", false
}

// skip applies the explicit exclusions: the opt-out annotation and excluded
// namespaces.
func skip(cfg Config, container Container) (reason string, ok bool) {
	if container.annotated(cfg.OptOutAnnotation, "false") {
		return "opt-out annotation", true
	}
	for _, namespace := range cfg.ExcludedNamespaces {
		if container.Namespace == namespace {
			return "excluded namespace", true
		}
	}
	return "", false
}

func hasOverlay(cfg Config, container Container) bool {
	for _, mount := range container.Mounts {
		if mount.Destination == cfg.ContainerOverlayPath {
			return true
		}
	}
	return false
}

// mountOverlay binds the staged mock driver tree into the container, read-only
// except for the config directory.
//
// Selected GPU and IMEX containers receive this whole shared tree.
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
