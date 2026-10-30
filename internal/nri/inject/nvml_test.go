// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/config"
)

// claimedFromDriverRoot is a container holding a DRA claim, with the mounts the
// DRA driver's CDI spec gives it from a driver root of NVIDIA's userspace.
func claimedFromDriverRoot() Container {
	const libDir = "/usr/lib/aarch64-linux-gnu"
	return Container{
		Namespace:  "gpu-operator",
		CDIDevices: []string{"k8s.gpu.nvidia.com/claim=claim-uid-gpu-0"},
		Mounts: []Mount{
			{Source: "/run/nvidia/driver" + libDir + "/libnvidia-ml.so.580.178.04", Destination: libDir + "/libnvidia-ml.so.580.178.04", Type: "bind"},
			{Source: "/run/nvidia/driver" + libDir + "/libnvidia-ml.so.1", Destination: libDir + "/libnvidia-ml.so.1", Type: "bind"},
			{Source: "/run/nvidia/driver" + libDir + "/libnvidia-ml.so", Destination: libDir + "/libnvidia-ml.so", Type: "bind"},
			{Source: "/run/nvidia/driver" + libDir + "/libcuda.so.580.178.04", Destination: libDir + "/libcuda.so.580.178.04", Type: "bind"},
			{Source: "/run/nvidia/driver/usr/bin/nvidia-smi", Destination: "/usr/bin/nvidia-smi", Type: "bind"},
		},
	}
}

// boundTo returns the destinations the adjustment binds the mock NVML at.
func boundTo(adjustment Adjustment) []string {
	var destinations []string
	for _, mount := range adjustment.Mounts {
		if mount.Source == "/var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1" {
			destinations = append(destinations, mount.Destination)
		}
	}
	return destinations
}

// In the kernel mode an allocation's CDI spec mounts the driver's real NVML, and
// a library whose RPATH names that directory loads it ahead of the overlay.
func TestAdjustBindsTheMockOverTheDriversNVMLInKernelMode(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.DriverMode = config.DriverModeKernel

	adjustment, ok := Adjust(cfg, claimedFromDriverRoot())
	require.True(t, ok)
	require.ElementsMatch(t, []string{
		"/usr/lib/aarch64-linux-gnu/libnvidia-ml.so.580.178.04",
		"/usr/lib/aarch64-linux-gnu/libnvidia-ml.so.1",
	}, boundTo(adjustment))
	require.Contains(t, adjustment.Mounts, Mount{
		Source:      "/var/lib/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1",
		Destination: "/usr/lib/aarch64-linux-gnu/libnvidia-ml.so.580.178.04",
		Type:        "bind",
		Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
	})
}

// Under the userspace driver the CDI specs come from the mock's own root, so
// the library they mount is the mock already.
func TestAdjustLeavesTheMountedNVMLInUserspaceMode(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(DefaultConfig(), claimedFromDriverRoot())
	require.True(t, ok)
	require.Empty(t, boundTo(adjustment))
}
