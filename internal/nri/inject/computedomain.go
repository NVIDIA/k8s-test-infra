// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/imex"
)

const (
	computeDomainContainerName = "compute-domain-daemon"
	computeDomainLabel         = "resource.nvidia.com/computeDomain"
	// The node agent publishes nvidia-imex.real last and withdraws it first,
	// so on disk it means the set it came with is complete.
	realIMEXRelPath       = imex.BinDir + "/" + imex.RealDaemonBinary
	realIMEXContainerPath = imex.ContainerBinDir + "/" + imex.RealDaemonBinary
)

// specializedWorkload replaces the generic overlay for a container that gets
// its driver footprint from elsewhere, such as the DRA driver's CDI edits.
type specializedWorkload struct {
	name string
	// matches also sees the configuration, so a workload whose feature is
	// off leaves its containers to the generic overlay.
	matches func(Config, Container) bool
	// adjust returns ok=false to leave the container unmodified. It never
	// fails: like the generic overlay, a specialized workload fails open.
	adjust func(Config, Container) (Adjustment, bool)
	// ungated workloads are adjusted without the node agent's staging gate,
	// because adjust itself tells a complete staged set from a partial one.
	ungated bool
}

var specializedWorkloads = []specializedWorkload{
	{name: computeDomainContainerName, matches: computeDomainDaemon, adjust: adjustComputeDomain, ungated: true},
	// Ungated: the simulated modules are not part of the node agent's tree.
	{name: driverManagerContainerName, matches: driverManager, adjust: adjustDriverManager, ungated: true},
	{name: driverContainerName, matches: driverContainer, adjust: adjustDriverContainer, ungated: true},
}

// Gated reports whether adjusting container waits for the node agent's staging
// gate. The ComputeDomain daemon does not: NRI sees a container only when it is
// created, and a daemon started behind a closed gate would have the shim but
// not nvidia-imex.real. The DRA daemon then restarts the shim inside its
// container, so NRI would not see it again until the driver's startup probe
// gives up, about 20 minutes later.
func Gated(cfg Config, container Container) bool {
	for i := range specializedWorkloads {
		if specializedWorkloads[i].matches(cfg, container) {
			return !specializedWorkloads[i].ungated
		}
	}
	return true
}

// computeDomainDaemon identifies the DRA-managed container from metadata NRI
// actually receives during CreateContainer. containerd resolves the pod's
// resource-claim CDI devices after this callback, so they cannot be the
// selector here. Requiring both the DRA-owned label and the container name
// avoids widening this adjustment to unrelated containers in the same
// namespace.
func computeDomainDaemon(_ Config, container Container) bool {
	return container.Name == computeDomainContainerName && container.PodLabels[computeDomainLabel] != ""
}

// adjustComputeDomain adds what the DRA driver's CDI edits leave out of its
// ComputeDomain daemon on a mock node: the real IMEX daemon that Mokka's
// nvidia-imex shim runs from beside itself. The rest of the driver footprint
// comes from those CDI edits, so the generic overlay is not applied a second
// time.
//
// It is mounted only while staged. Without IMEX the daemon fails as it does on
// a real node without the IMEX package; the container itself is never refused.
func adjustComputeDomain(cfg Config, container Container) (Adjustment, bool) {
	if !regularFile(filepath.Join(cfg.HostOverlayPath, realIMEXRelPath)) {
		zap.L().Warn("IMEX node software is not staged; the ComputeDomain daemon gets no nvidia-imex.real and fails as on a node without IMEX",
			zap.String("namespace", container.Namespace), zap.String("container", container.Name))
		return Adjustment{}, false
	}
	zap.L().Debug("adjusting the ComputeDomain daemon", zap.String("namespace", container.Namespace))
	return Adjustment{Mounts: []Mount{
		readOnlyFileMount(filepath.Join(cfg.HostOverlayPath, realIMEXRelPath), realIMEXContainerPath),
	}}, true
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// readOnlyFileMount binds one staged file read-only. It is rprivate for the
// same reason as the overlay binds: without it, a container with an rshared
// root would share the bind's mount events with the node.
func readOnlyFileMount(source, destination string) Mount {
	return Mount{
		Source:      source,
		Destination: destination,
		Type:        "bind",
		Options:     []string{"rbind", "rprivate", "ro", "nosuid", "nodev"},
	}
}
