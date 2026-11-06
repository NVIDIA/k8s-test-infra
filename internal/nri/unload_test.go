// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// callerName is a caller whose delete_module names name, or whose memory
// cannot be read with err.
type callerName struct {
	name string
	err  error
}

func (c callerName) String(uint64, int) (string, error) { return c.name, c.err }

func moduleTree(t *testing.T, modules ...string) string {
	t.Helper()

	root := t.TempDir()
	for _, module := range modules {
		require.NoError(t, os.MkdirAll(filepath.Join(root, module, "holders"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, module, "refcnt"), []byte("0\n"), 0o644))
	}
	return root
}

func TestUnloadAnswersAsTheKernel(t *testing.T) {
	t.Parallel()

	root := moduleTree(t, "nvidia")

	val, errno := unload(root, "gpu-operator/k8s-driver-manager", 0, callerName{name: "nvidia"})
	require.Zero(t, val)
	require.Zero(t, errno)
	require.NoDirExists(t, filepath.Join(root, "nvidia"))

	val, errno = unload(root, "gpu-operator/k8s-driver-manager", 0, callerName{name: "nvidia"})
	require.Equal(t, int64(-1), val)
	require.Equal(t, unix.ENOENT, errno)
}

func TestUnloadFailsAnUnreadableName(t *testing.T) {
	t.Parallel()

	root := moduleTree(t, "nvidia")

	_, errno := unload(root, "gpu-operator/k8s-driver-manager", 0, callerName{err: errors.New("caller left the syscall")})

	require.Equal(t, unix.EFAULT, errno)
	require.DirExists(t, filepath.Join(root, "nvidia"))
}

// An error that is not the kernel's must not pass for one: EIO says the
// unload did not happen for a reason the caller cannot act on.
func TestUnloadFailsWithEIOOnABrokenTree(t *testing.T) {
	t.Parallel()

	root := moduleTree(t, "nvidia")
	require.NoError(t, os.WriteFile(filepath.Join(root, "nvidia", "refcnt"), []byte("live\n"), 0o644))

	_, errno := unload(root, "gpu-operator/k8s-driver-manager", 0, callerName{name: "nvidia"})

	require.Equal(t, unix.EIO, errno)
}

// A filesystem errno from the tree is not the kernel refusing the unload.
func TestUnloadFailsWithEIOOnAnUnreadableTree(t *testing.T) {
	t.Parallel()

	root := moduleTree(t, "nvidia")
	holders := filepath.Join(root, "nvidia", "holders")
	require.NoError(t, os.Remove(holders))
	require.NoError(t, os.WriteFile(holders, nil, 0o644))

	_, errno := unload(root, "gpu-operator/k8s-driver-manager", 0, callerName{name: "nvidia"})

	require.Equal(t, unix.EIO, errno)
	require.DirExists(t, filepath.Join(root, "nvidia"))
}
