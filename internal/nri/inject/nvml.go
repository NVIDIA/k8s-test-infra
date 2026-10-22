// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"path"
	"path/filepath"
	"regexp"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/config"
)

// driverNVMLName matches the NVML library files a CDI spec mounts from a driver
// root: the versioned file and libnvidia-ml.so.1. The bare libnvidia-ml.so is a
// link for build-time linkers, which nothing loads at run time.
var driverNVMLName = regexp.MustCompile(`^libnvidia-ml\.so(\.[0-9]+)+$`)

// bindMockNVML puts the mock NVML at every path the incoming spec of a GPU
// container mounts the driver's own library at.
//
// In the kernel mode the driver root holds NVIDIA's real userspace, and the CDI
// spec of a GPU allocation, whether the DRA driver or the device plugin wrote
// it, mounts the real library into the container. The overlay's LD_LIBRARY_PATH
// wins a lookup by name, but the loader searches a library's RPATH first:
// DCGM's libdcgm.so.4 names its own directory, where the spec mounts the real
// NVML, so dcgm-exporter loaded that one and failed on the simulated modules.
// NRI replaces a mount at the same destination, so the mock bound there is what
// every lookup finds, through an RPATH, the spec's links or the loader cache.
// Under the userspace driver the specs come from the mock's own root and mount
// the mock already.
func bindMockNVML(cfg Config, container Container, sel selection, adjustment *Adjustment) {
	if !sel.gpu() || cfg.DriverMode != config.DriverModeKernel {
		return
	}
	for _, mount := range container.Mounts {
		if !driverNVMLName.MatchString(path.Base(mount.Destination)) {
			continue
		}
		zap.L().Debug("binding the mock NVML over the driver's",
			zap.String("namespace", container.Namespace), zap.String("path", mount.Destination))
		adjustment.Mounts = append(adjustment.Mounts, Mount{
			Source:      filepath.Join(cfg.HostOverlayPath, mockNVMLRelPath),
			Destination: mount.Destination,
			Type:        "bind",
			Options:     []string{"bind", "rprivate", "ro", "nosuid", "nodev"},
		})
	}
}
