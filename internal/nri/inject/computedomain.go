// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	computeDomainContainerName = "compute-domain-daemon"
	computeDomainLabel         = "resource.nvidia.com/computeDomain"
	realIMEXRelPath            = "driver/usr/bin/nvidia-imex.real"
	realIMEXContainerPath      = "/usr/bin/nvidia-imex.real"
)

// computeDomainDaemon identifies the DRA-managed container from metadata NRI
// actually receives during CreateContainer. containerd resolves the pod's
// resource-claim CDI devices after this callback, so they cannot be the
// selector here. Requiring both the DRA-owned label and container name avoids
// widening this adjustment to unrelated containers in the same namespace.
func computeDomainDaemon(container Container) bool {
	return container.Name == computeDomainContainerName && container.PodLabels[computeDomainLabel] != ""
}

// adjustComputeDomain adds only the mock-specific files absent from the
// upstream DRA CDI edits. This avoids applying Mokka's ambient driver overlay a
// second time to a container whose driver footprint is already CDI-managed.
func adjustComputeDomain(cfg Config, container Container, adjustment *Adjustment) error {
	realIMEX := filepath.Join(cfg.HostOverlayPath, realIMEXRelPath)
	if !regularFile(realIMEX) {
		return fmt.Errorf("compute domain prerequisite %s is not staged", realIMEX)
	}
	if !topologyInjectable(cfg) {
		return fmt.Errorf("compute domain topology %s is not staged for node %q", cfg.TopologyHostPath, cfg.NodeName)
	}

	adjustment.Mounts = append(adjustment.Mounts,
		readOnlyFileMount(realIMEX, realIMEXContainerPath),
		readOnlyFileMount(cfg.TopologyHostPath, cfg.TopologyContainerPath))
	env := newEnvSet(container.Env)
	env.setDefault("MOCK_TOPOLOGY_CONFIG", cfg.TopologyContainerPath)
	adjustment.Env = append(adjustment.Env, env.changed()...)
	return nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func readOnlyFileMount(source, destination string) Mount {
	return Mount{
		Source:      source,
		Destination: destination,
		Type:        "bind",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	}
}
