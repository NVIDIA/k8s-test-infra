// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// classRel is where the renderer puts the classes, relative to the IB root.
const classRel = "sys/class"

// mockOwnedClasses are the classes the renderer writes. The host's versions
// must never shadow them: a CPU-only node has no InfiniBand, so carrying the
// host's across would retract every simulated HCA.
//
// net is deliberately absent. The agent's interfaces are real kernel links, so
// they appear in the host's own class and carrying it across shows them
// alongside the node's real interfaces — whereas rendering a mock net class
// would hide eth0 from every served pod.
var mockOwnedClasses = []string{
	"infiniband",
	"infiniband_mad",
	"infiniband_verbs",
	"infiniband_devices",
}

// classesToReproduce lists the host classes to carry into the served tree.
// Consumers read classes the mock has no reason to render, and a pod served
// this tree sees its sys/class in place of the node's, so anything not carried
// across simply vanishes for them.
func classesToReproduce(hostClassDir string, owned []string) ([]string, error) {
	entries, err := os.ReadDir(hostClassDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", hostClassDir, err)
	}

	skip := make(map[string]struct{}, len(owned))
	for _, c := range owned {
		skip[c] = struct{}{}
	}

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		if _, ok := skip[e.Name()]; ok {
			continue
		}

		out = append(out, e.Name())
	}

	return out, nil
}

// mountRealClasses binds each host class onto the matching directory the
// renderer prepared inside the tree. The mountpoints are the renderer's, so
// they survive its pruning; this only attaches to them.
//
// Read-only, because nothing served this tree has any business writing to the
// node's sysfs.
func mountRealClasses(hostSysClass, treeClass string, classes []string) error {
	errs := make([]error, 0, len(classes))

	for _, c := range classes {
		src, dst := filepath.Join(hostSysClass, c), filepath.Join(treeClass, c)
		if err := bindReadOnly(src, dst); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// unmountRealClasses detaches the binds, leaving the mountpoint directories in
// place: a consumer whose own mount outlives an agent restart keeps pointing at
// inodes that still exist. See NVIDIA/k8s-test-infra#742.
//
// The tree names what to detach, so this stays the exact inverse of whatever
// the last pass attached, even across a restart that changed the profile or
// found the node's classes different.
func unmountRealClasses(treeClass string) error {
	classes, err := classesToReproduce(treeClass, mockOwnedClasses)
	if err != nil {
		if os.IsNotExist(errors.Unwrap(err)) {
			return nil // no tree, so nothing was ever attached
		}

		return err
	}

	errs := make([]error, 0, len(classes))

	for _, c := range classes {
		if err := unmountAt(filepath.Join(treeClass, c)); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
