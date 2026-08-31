// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
