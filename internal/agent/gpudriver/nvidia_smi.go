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

// pick returns the bundled nvidia-smi for driver and its version, as
// chooseNvidiaSMI picks it.
func (img nvidiaSMIImage) pick(driver string) (string, string, error) {
	versions, err := img.bundled()
	if err != nil {
		return "", "", err
	}
	chosen := chooseNvidiaSMI(versions, driver)
	return filepath.Join(img.bundledDir, chosen, nvidiaSMIName), chosen, nil
}

// bundled lists the versions of the nvidia-smi binaries img ships, oldest
// first.
func (img nvidiaSMIImage) bundled() ([]string, error) {
	entries, err := os.ReadDir(img.bundledDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoBundledNvidiaSMI, err)
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
		return nil, fmt.Errorf("%w in %s", errNoBundledNvidiaSMI, img.bundledDir)
	}
	slices.SortFunc(versions, engine.CompareDriverVersions)
	return versions, nil
}

// chooseNvidiaSMI picks, from versions sorted oldest first, the nvidia-smi a
// node on driver runs. A real node's nvidia-smi comes with its driver, and the
// NVML field ids and NVLink count it reads changed between branches, so a
// binary from another branch misreads the mock's answers.
//
//   - Within the driver's branch: the newest binary not newer than the driver,
//     or the branch's oldest when every one is newer. A binary of the same
//     branch addresses the same NVLinks, which an older branch's may not.
//   - A driver with no binary of its branch: the newest of the newest older
//     branch. NVML serves callers built for older drivers, never newer ones.
//   - A driver older than every bundled branch, or none: the oldest.
func chooseNvidiaSMI(versions []string, driver string) string {
	want, ok := branchOf(driver)
	if !ok {
		return versions[0]
	}
	// versions is ascending, so the last match of each kind is the newest.
	var sameBranch, olderBranch string
	for _, v := range versions {
		switch b, _ := branchOf(v); {
		case b < want:
			olderBranch = v
		case b == want && (sameBranch == "" || engine.CompareDriverVersions(v, driver) <= 0):
			sameBranch = v
		}
	}
	switch {
	case sameBranch != "":
		return sameBranch
	case olderBranch != "":
		return olderBranch
	default:
		return versions[0]
	}
}

// repoint points link at target in one rename, so link keeps its old target
// when the new one cannot be made.
func repoint(target, link string) error {
	tmp := link + ".staging"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", tmp, target, err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s to %s: %w", tmp, link, err)
	}
	return nil
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
