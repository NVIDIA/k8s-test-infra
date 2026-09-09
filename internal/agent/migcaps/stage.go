// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package migcaps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/internal/migcaps"
)

// Paths are relative to host.Root and mirror the driver root a consumer is
// pointed at, so nvidia-container-toolkit's NewMigCapsFromRoot finds the table
// at the path it expects.
const (
	capDevDir       = "driver/dev/nvidia-caps"
	minorsDir       = "driver/proc/driver/nvidia-caps"
	capabilitiesDir = "driver/proc/driver/nvidia/capabilities"
)

// capsFor translates the agent's compiled MIG state into the capability table.
func capsFor(state agent.MIGState) []migcaps.Cap {
	gpus := make([]migcaps.GPU, 0, len(state.GPUs))
	for _, gpu := range state.GPUs {
		instances := make([]migcaps.GPUInstance, 0, len(gpu.GPUInstances))
		for _, gi := range gpu.GPUInstances {
			instances = append(instances, migcaps.GPUInstance{
				ID:                 gi.ID,
				ComputeInstanceIDs: gi.ComputeInstanceIDs,
			})
		}
		gpus = append(gpus, migcaps.GPU{Minor: gpu.Minor, GPUInstances: instances})
	}
	return migcaps.Caps(gpus)
}

// stageCapDevs creates the chardevs that guard each MIG partition. The nodes
// must exist on disk before containerd will admit a container that asks for
// them, which is what the device plugin's device specs do.
func stageCapDevs(h *host.Host, major int, caps []migcaps.Cap) error {
	dir := filepath.Join(h.Root, capDevDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if major <= 0 {
		return fmt.Errorf("nvidia-caps device major %d is not usable", major)
	}

	for _, c := range caps {
		path := filepath.Join(dir, fmt.Sprintf("nvidia-cap%d", c.Minor))
		if err := fsutil.Mknod(path, uint32(major), uint32(c.Minor)); err != nil { //nolint:gosec // both are validated small positives
			return fmt.Errorf("nvidia-cap%d: %w", c.Minor, err)
		}
	}
	return nil
}

// stageMinors writes the table mapping capability names to cap device minors.
func stageMinors(h *host.Host, caps []migcaps.Cap) error {
	path := filepath.Join(h.Root, minorsDir, "mig-minors")
	return fsutil.Write(path, []byte(migcaps.Minors(caps)), 0o644)
}

// stageCapabilityTree writes the per-capability procfs files. A consumer reads
// DeviceFileMinor from these to find the cap device without going through
// mig-minors, so the two have to agree.
func stageCapabilityTree(h *host.Host, caps []migcaps.Cap) error {
	for _, c := range caps {
		path := filepath.Join(h.Root, capabilitiesDir, capabilityProcPath(c.Name))
		if err := fsutil.Write(path, []byte(capabilityFile(c.Minor)), 0o644); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
	}
	return nil
}

// capabilityProcPath maps a capability name to its path under capabilities/,
// mirroring nvidia-container-toolkit's MigCap.ProcPath: the node-wide caps live
// under mig/, and a GPU's caps under gpu<N>/mig/.
func capabilityProcPath(name string) string {
	switch name {
	case "config", "monitor":
		return filepath.Join("mig", name)
	}
	gpu, rest, found := strings.Cut(name, "/")
	if !found {
		return name
	}
	return filepath.Join(gpu, "mig", rest)
}

// capabilityFile renders a capability file's body. Mode 292 is 0444, and
// Modify 1 is what the driver reports for a capability the caller may use;
// both are copied from a real MIG-enabled node.
func capabilityFile(minor int) string {
	return fmt.Sprintf("DeviceFileMinor: %d\nDeviceFileMode: 292\nDeviceFileModify: 1\n", minor)
}
