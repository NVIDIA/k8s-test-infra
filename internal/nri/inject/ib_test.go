// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// overlayWithIBTree stages the directories the node agent renders when a
// profile declares InfiniBand, and returns a config rooted at them.
func overlayWithIBTree(t *testing.T) Config {
	t.Helper()

	root := t.TempDir()
	for _, rel := range []string{ibSysClassRelPath, ibDevicesRelPath} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
	}

	return Config{HostOverlayPath: root}
}

func mountFor(t *testing.T, mounts []Mount, destination string) Mount {
	t.Helper()

	for _, m := range mounts {
		if m.Destination == destination {
			return m
		}
	}

	require.FailNowf(t, "mount missing", "no mount at %s", destination)

	return Mount{}
}

// The shims redirect the kernel paths for libc callers, but a Go consumer
// issues openat directly and reads the node's real /sys, where a CPU-only node
// has no InfiniBand. The RDMA device plugin is such a consumer, so the tree has
// to be served at the paths it actually reads. Same reasoning as the CDI PCI
// sysfs mounts in #673, through a different channel: the plugin pod requests no
// GPU, so no CDI spec is ever resolved for it.
func TestAdjust_ServesTheIBTreeAtTheKernelPaths(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(overlayWithIBTree(t), Container{Namespace: "default"})
	require.True(t, ok)

	sysClass := mountFor(t, adjustment.Mounts, "/sys/class")
	// rbind, not bind: the agent bind-mounts the node's own classes inside this
	// directory, and a plain bind would leave every one of them empty in the
	// container.
	require.Contains(t, sysClass.Options, "rbind")
	require.Contains(t, sysClass.Options, "ro")

	devices := mountFor(t, adjustment.Mounts, "/dev/infiniband")
	require.Contains(t, devices.Options, "rbind")
	// nodev would make the kernel refuse to open the device nodes behind this
	// mount, which is the only reason they are rendered at all.
	require.NotContains(t, devices.Options, "nodev")
}

// Nothing orders the plugin's DaemonSet after the agent's, and a mount whose
// source does not exist fails creation for the whole pod. A tier that renders
// no tree therefore has to degrade the injection, not break every container on
// the node.
func TestAdjust_OmitsTheKernelPathsWhenNoTreeIsstaged(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(Config{HostOverlayPath: t.TempDir()}, Container{Namespace: "default"})
	require.True(t, ok)

	for _, m := range adjustment.Mounts {
		require.NotEqual(t, "/sys/class", m.Destination)
		require.NotEqual(t, "/dev/infiniband", m.Destination)
	}
}

// The two halves are staged by separate renderers, so one arriving first must
// not hold the other back.
func TestAdjust_ServesWhicheverKernelPathIsStaged(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ibSysClassRelPath), 0o755))

	adjustment, ok := Adjust(Config{HostOverlayPath: root}, Container{Namespace: "default"})
	require.True(t, ok)

	require.Equal(t, "/sys/class", mountFor(t, adjustment.Mounts, "/sys/class").Destination)

	for _, m := range adjustment.Mounts {
		require.NotEqual(t, "/dev/infiniband", m.Destination)
	}
}
