// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func skipUnlessRootLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Skip("requires root on Linux (mknod)")
	}
}

func TestMknod_CreatesChardev(t *testing.T) {
	skipUnlessRootLinux(t)

	h := New(t.TempDir())
	path := filepath.Join(h.Root, "test-chardev")

	require.NoError(t, h.Mknod(path, 195, 0))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.EqualValues(t, 0o666, fi.Mode().Perm())
}

func TestMknod_Idempotent(t *testing.T) {
	skipUnlessRootLinux(t)

	h := New(t.TempDir())
	path := filepath.Join(h.Root, "test-chardev")

	require.NoError(t, h.Mknod(path, 195, 0))
	require.NoError(t, h.Mknod(path, 195, 0), "second Mknod must not error")
}

func TestHostRemove(t *testing.T) {
	t.Run("existing file is removed", func(t *testing.T) {
		h := New(t.TempDir())
		p := filepath.Join(h.Root, "file.txt")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))

		require.NoError(t, h.Remove(p))
		_, err := os.Stat(p)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("absent path is not an error", func(t *testing.T) {
		h := New(t.TempDir())
		require.NoError(t, h.Remove(filepath.Join(h.Root, "does-not-exist")))
	})
}

func TestPathAccessors(t *testing.T) {
	h := New("/host")

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"root no parts", h.RootPath(), "/host/var/lib/nvml-mock"},
		{"root one part", h.RootPath("ib"), "/host/var/lib/nvml-mock/ib"},
		{"root many parts", h.RootPath("driver/usr/bin", "ibstat"), "/host/var/lib/nvml-mock/driver/usr/bin/ibstat"},
		{"dev", h.DevPath("nvidia0"), "/host/dev/nvidia0"},
		{"proc", h.ProcPath("devices"), "/host/proc/devices"},
		{"sys", h.SysPath("bus/pci"), "/host/sys/bus/pci"},
		{"etc", h.EtcPath("libibverbs.d"), "/host/etc/libibverbs.d"},
		{"run", h.RunPath("nvidia"), "/host/run/nvidia"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, c.got)
		})
	}
}

// The accessors must agree with the fields they join under, so a caller can mix
// both without the two drifting apart.
func TestPathAccessors_MatchFields(t *testing.T) {
	h := New(t.TempDir())
	require.Equal(t, h.Root, h.RootPath())
	require.Equal(t, filepath.Join(h.Root, "a", "b"), h.RootPath("a", "b"))
}
