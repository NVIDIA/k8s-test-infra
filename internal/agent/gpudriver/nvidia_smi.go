// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// nvidiaSMIImage locates the nvidia-smi binaries the image ships and the one on
// the node agent's own PATH. deployments/nvml-mock/Dockerfile lays them out.
type nvidiaSMIImage struct {
	// bundledDir holds one directory per nvidia-smi, named after the driver
	// version it came from: <bundledDir>/580.65.06/nvidia-smi.
	bundledDir string
	// podPath is the nvidia-smi `kubectl exec` into the node agent runs. The
	// image points it at the oldest bundled binary; staging points it at the
	// staged one, so the pod runs the nvidia-smi it serves.
	podPath string
}

// defaultNvidiaSMIImage is the layout of the nvml-mock image.
var defaultNvidiaSMIImage = nvidiaSMIImage{
	bundledDir: "/usr/local/libexec/nvml-mock/nvidia-smi",
	podPath:    "/usr/local/bin/nvidia-smi",
}

// nvidiaSMIName is the binary's file name in every directory that holds one.
const nvidiaSMIName = "nvidia-smi"

var errNoBundledNvidiaSMI = errors.New("no bundled nvidia-smi")

// pick returns the bundled nvidia-smi for driver and its version: the newest
// whose branch is not newer than the driver's. A real node's nvidia-smi comes
// with its driver, and the NVML field ids and NVLink count it reads changed
// between branches, so a binary from another branch misreads the mock's
// answers. NVML serves callers built for older drivers, never newer ones, which
// is why an older branch is the fallback. A driver older than every bundled
// branch gets the oldest.
func (img nvidiaSMIImage) pick(driver string) (string, string, error) {
	entries, err := os.ReadDir(img.bundledDir)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", errNoBundledNvidiaSMI, err)
	}
	var versions []string
	for _, e := range entries {
		if _, ok := branchOf(e.Name()); !ok {
			continue
		}
		if fi, err := os.Stat(filepath.Join(img.bundledDir, e.Name(), nvidiaSMIName)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		versions = append(versions, e.Name())
	}
	if len(versions) == 0 {
		return "", "", fmt.Errorf("%w in %s", errNoBundledNvidiaSMI, img.bundledDir)
	}
	slices.SortFunc(versions, engine.CompareDriverVersions)

	chosen := versions[0]
	if want, ok := branchOf(driver); ok {
		for _, v := range versions {
			if b, _ := branchOf(v); b <= want {
				chosen = v
			}
		}
	}
	return filepath.Join(img.bundledDir, chosen, nvidiaSMIName), chosen, nil
}

// branchOf returns the driver branch of a version string: 580 for 580.65.06.
func branchOf(version string) (int, bool) {
	major, _, _ := strings.Cut(version, ".")
	b, err := strconv.Atoi(major)
	if err != nil || b <= 0 {
		return 0, false
	}
	return b, true
}
