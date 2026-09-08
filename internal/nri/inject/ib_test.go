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
	require.Contains(t, sysClass.Options, "ro")

	devices := mountFor(t, adjustment.Mounts, "/dev/infiniband")
	// nodev would make the kernel refuse to open the device nodes behind this
	// mount, which is the only reason they are rendered at all.
	require.NotContains(t, devices.Options, "nodev")
}

// Binding a shared mount makes the copy a peer of it, so every mount the
// runtime then makes underneath propagates back onto the node's own path. The
// node's /sys is shared, and pods using bidirectional propagation carry the
// escape all the way out: each class the plugin served reappeared on the node,
// and the next container to bind that path copied the accumulated stack, so it
// doubled per container generation. Seven generations put 127 mounts on every
// class entry and ~21k in the node's mount table, past which containerd could
// no longer tear pods down and every pod hung terminating.
//
// Private detaches the copy from the node's group so nothing propagates back.
// Non-recursive keeps a stack that did accumulate from being copied on, and
// costs nothing: the classes the mock tree hides are served back as mounts of
// their own, not as submounts of the tree.
func TestAdjust_KeepsInjectedMountsOutOfTheNodesPropagationGroup(t *testing.T) {
	t.Parallel()

	adjustment, ok := Adjust(overlayWithIBTree(t), Container{Namespace: "default"})
	require.True(t, ok)
	require.NotEmpty(t, adjustment.Mounts)

	for _, m := range adjustment.Mounts {
		require.Containsf(t, m.Options, "rprivate", "mount at %s can propagate to the node", m.Destination)
		require.NotContainsf(t, m.Options, "rbind", "recursive bind at %s copies whatever leaked", m.Destination)
	}
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

// The class mount replaces a container's whole sys/class, so the node's other
// classes have to be served back over the mountpoints the renderer left for
// them. sys/class/net is the one that matters most: an RDMA consumer resolves
// every HCA through an interface listed there.
func TestAdjust_ServesTheNodesOwnClassesBackOverTheMockOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, rel := range []string{
		filepath.Join(ibSysClassRelPath, "infiniband"),
		filepath.Join(ibSysClassRelPath, "net"),
		filepath.Join(ibSysClassRelPath, "block"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
	}

	adjustment, ok := Adjust(Config{HostOverlayPath: root}, Container{Namespace: "default"})
	require.True(t, ok)

	net := mountFor(t, adjustment.Mounts, "/sys/class/net")
	require.Equal(t, "/sys/class/net", net.Source, "the node's own class is the source")
	require.Equal(t, "/sys/class/block", mountFor(t, adjustment.Mounts, "/sys/class/block").Source)

	// The mock's own classes must not be served back from the node: a CPU-only
	// node's infiniband class is empty, and it would cover every rendered HCA.
	for _, m := range adjustment.Mounts {
		require.NotEqual(t, "/sys/class/infiniband", m.Destination)
	}

	// Each reproduced class has to land inside the class mount, so it must be
	// emitted after it.
	classIdx, netIdx := -1, -1
	for i, m := range adjustment.Mounts {
		switch m.Destination {
		case "/sys/class":
			classIdx = i
		case "/sys/class/net":
			netIdx = i
		}
	}
	require.Less(t, classIdx, netIdx, "the covering mount must come first")
}
