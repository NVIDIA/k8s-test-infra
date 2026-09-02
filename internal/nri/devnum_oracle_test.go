// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import (
	"runtime"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// unix.Major and unix.Minor decode the dev_t of the platform they are compiled
// for, so they are only the right oracle on Linux. The file carries no
// //go:build linux tag on purpose: it then still type-checks and lints on a
// macOS workstation, and skips at run time instead of disappearing.
func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("unix.Major/unix.Minor decode %s dev_t, not Linux dev_t", runtime.GOOS)
	}
}

// major and minor reimplement glibc's encoding rather than calling unix.Major
// and unix.Minor, because stat.Rdev always describes a Linux device node
// whatever host the binary was built on. This pins the local implementation to
// the library on the platform where the library agrees, so the two cannot
// drift.
func TestMajorMinorAgreeWithUnixOnLinux(t *testing.T) {
	requireLinux(t)
	t.Parallel()

	// Each bit in isolation, so every bit's routing between the major and the
	// minor field is checked on its own. Bits 44-63 and 20-43 are the ones a
	// truncating mask drops.
	for bit := range 64 {
		dev := uint64(1) << bit
		require.Equal(t, uint64(unix.Major(dev)), major(dev), "major(1<<%d)", bit)
		require.Equal(t, uint64(unix.Minor(dev)), minor(dev), "minor(1<<%d)", bit)
	}

	for _, dev := range []uint64{
		0x0,
		0x103,              // /dev/null
		0x1ff05,            // major 511, minor 5
		0x123459abcde678f0, // every field populated
		0xffffffffffffffff, // all bits set
		0xfffff00000000000, // extended major field only
		0x00000ffffff00000, // extended minor field only
		0x00000000000fff00, // legacy major field only
		0x00000000000000ff, // legacy minor field only
	} {
		require.Equal(t, uint64(unix.Major(dev)), major(dev), "major(%#x)", dev)
		require.Equal(t, uint64(unix.Minor(dev)), minor(dev), "minor(%#x)", dev)
	}
}

// The device numbers below are fixed by the Linux mem driver, so they are
// literals rather than values read back from the same syscall the code uses.
func TestLinuxDeviceDecodesRealCharacterDevices(t *testing.T) {
	requireLinux(t)
	t.Parallel()

	tests := []struct {
		name      string
		hostPath  string
		wantMajor int64
		wantMinor int64
	}{
		{name: "/dev/null", hostPath: "/dev/null", wantMajor: 1, wantMinor: 3},
		{name: "/dev/zero", hostPath: "/dev/zero", wantMajor: 1, wantMinor: 5},
		{name: "/dev/full", hostPath: "/dev/full", wantMajor: 1, wantMinor: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			device, err := linuxDevice(inject.Device{HostPath: tt.hostPath, Path: "/dev/nvidia0"})
			require.NoError(t, err)
			require.NotNil(t, device)

			// Catches decoding stat.Rdev with the wrong encoding: containerd
			// creates the device node from these two numbers, so a wrong value
			// gives the container a node pointing at a different driver.
			require.Equal(t, tt.wantMajor, device.Major)
			require.Equal(t, tt.wantMinor, device.Minor)
			require.Equal(t, "c", device.Type)
			require.Equal(t, "/dev/nvidia0", device.Path)
		})
	}
}
