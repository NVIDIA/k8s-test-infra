// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package inject decides what the nvml-mock overlay adds to a container. It is
// the runtime-neutral half of the NRI plugin: no containerd types cross this
// boundary, so the decision is exercisable as a plain table test.
//
// Every step fails open. A missing surface degrades injection instead of
// failing container creation; that holds for a specialized workload too. The
// runtime-facing plugin separately gates adjustments while the node agent is
// staging or unavailable.
package inject

import (
	"path/filepath"

	"go.uber.org/zap"
)

// Decision is what to do with a container, decided once from the
// container alone, so the plugin can read it before it consults the node agent.
type Decision struct {
	cfg       Config
	container Container
	sel       selection
	// Skipped leaves the container exactly as authored, for Reason.
	Skipped bool
	Reason  string
}

// Decide decides what to do with container.
func Decide(cfg Config, container Container) Decision {
	cfg = withDefaults(cfg)
	sel, reason, skipped := decide(cfg, container)
	return Decision{cfg: cfg, container: container, sel: sel, Skipped: skipped, Reason: reason}
}

// Adjust returns what to add to the container, or ok=false when it should be
// left exactly as authored. It routes a container a specialized workload
// recognizes to that workload, and every other one to the generic overlay.
//
// The steps run in a fixed order, each contributing to the same adjustment.
// None of them can fail, which is why there is no error to return.
func (d Decision) Adjust() (Adjustment, bool) {
	cfg, container, sel := d.cfg, d.container, d.sel
	if d.Skipped {
		zap.L().Debug("skipping container injection", zap.String("namespace", container.Namespace), zap.String("reason", d.Reason))
		return Adjustment{}, false
	}
	if sel.workload != nil {
		return sel.workload.adjust(cfg, container)
	}
	if sel.allocated && container.annotated(cfg.DeviceAnnotation, "true") {
		zap.L().Debug("GPU allocation takes precedence over the devices annotation",
			zap.String("namespace", container.Namespace))
	}

	adjustment := adjustSelected(cfg, container, sel)

	zap.L().Debug("injecting container",
		zap.String("namespace", container.Namespace),
		zap.Int("mounts", len(adjustment.Mounts)),
		zap.Int("devices", len(adjustment.Devices)))

	return adjustment, true
}

// adjustSelected adds each surface the selection asked for.
func adjustSelected(cfg Config, container Container, sel selection) Adjustment {
	// IMEX channels are plain device nodes. Mounting the overlay for them alone
	// would let mock NVML enumerate every GPU on the node.
	var adjustment Adjustment
	if sel.gpu() || sel.infiniband {
		mountOverlay(cfg, &adjustment)
		setEnvironment(cfg, container, sel, &adjustment)
	}
	if sel.management {
		attachGPUs(cfg, &adjustment)
	}
	if sel.infiniband {
		warnUnlessHCAsStaged(cfg)
	}
	if sel.imex {
		attachIMEXChannels(cfg, &adjustment)
	}
	if sel.modules {
		adjustDriverModules(cfg, container, &adjustment)
	}
	return adjustment
}

// selection records which surfaces a container asked for. It is derived from
// the container alone, never from the node's staged tree. GPU, InfiniBand and
// IMEX are chosen independently; none of them implies another.
type selection struct {
	// allocated: the container holds a device-plugin or DRA GPU allocation,
	// which it keeps exactly.
	allocated bool
	// management: the devices annotation without an allocation, which
	// delivers every staged GPU.
	management bool
	// infiniband: the InfiniBand annotation only. A privileged container
	// inherits every /dev/infiniband node under a wildcard rule without asking
	// for RDMA, so device paths are not evidence of a request.
	infiniband bool
	imex       bool
	// modules: the simulated /sys/module, see wantsDriverModules.
	modules bool
	// workload is set for a container a specialized workload recognizes. Such
	// a container is routed before the opt-in checks: the DRA ComputeDomain
	// daemon carries no allocation or annotation at CreateContainer.
	workload *specializedWorkload
}

// decide is the single source of truth behind Decision.
//
// The mount check is what makes re-adjustment safe: a container that already
// carries the overlay at its destination has been through here before, and
// injecting a second time would stack duplicate LD_PRELOAD entries.
func decide(cfg Config, container Container) (sel selection, reason string, skipped bool) {
	if reason, skipped := skip(cfg, container); skipped {
		return selection{}, reason, true
	}
	for i := range specializedWorkloads {
		if specializedWorkloads[i].matches(cfg, container) {
			return selection{workload: &specializedWorkloads[i]}, "", false
		}
	}
	allocated := hasGPUAllocation(container)
	sel = selection{
		allocated:  allocated,
		management: container.annotated(cfg.DeviceAnnotation, "true") && !allocated,
		infiniband: container.annotated(cfg.InfiniBandAnnotation, "true"),
		imex:       container.annotated(cfg.ImexChannelAnnotation, "true"),
		modules:    wantsDriverModules(cfg, container),
	}
	if !sel.gpu() && !sel.infiniband && !sel.imex && !sel.modules {
		return sel, "no GPU allocation and no opt-in annotation", true
	}
	if hasOverlay(cfg, container) {
		return sel, "overlay already mounted", true
	}
	return sel, "", false
}

func (s selection) gpu() bool { return s.allocated || s.management }

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
// GPU and InfiniBand selections share this tree: the IB tools and shims are
// staged beside the mock driver. The environment decides which of the two is
// active.
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
//
// Both binds are rprivate, the propagation a hostPath volume without
// mountPropagation gets. A container with a Bidirectional volume has an rshared
// root, and while these binds named no propagation the overlay joined the peer
// group of the node mount it came from. The config bind layered on it was then
// copied onto the node, where the copies outlived the pod and doubled with each
// such container. rslave would stop that too, but the runtime accepts it only
// when the node mount holding the overlay is shared or slave, and fails
// container creation otherwise.
func mountOverlay(cfg Config, adjustment *Adjustment) {
	adjustment.Mounts = append(adjustment.Mounts,
		Mount{
			Source:      cfg.HostOverlayPath,
			Destination: cfg.ContainerOverlayPath,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
		},
		Mount{
			Source:      filepath.Join(cfg.HostOverlayPath, configRelPath),
			Destination: filepath.Join(cfg.ContainerOverlayPath, configRelPath),
			Type:        "bind",
			Options:     []string{"rbind", "rprivate", "rw", "nosuid", "nodev"},
		},
	)
}
