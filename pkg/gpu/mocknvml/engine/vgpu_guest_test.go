// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func vgpuGuestVirtualization(license *VGPULicenseConfig) *VirtualizationConfig {
	return &VirtualizationConfig{
		Mode: VirtualizationModeVGPU,
		VGPU: &VGPUConfig{Guest: &VGPUGuestConfig{
			HostDriverVersion: "550.163.01", HostDriverBranch: "r550_00", License: license,
		}},
	}
}

func cString(b []int8) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(b[i])
	}
	return string(out)
}

func TestVgpuGuest_LicensedWithExpiry(t *testing.T) {
	t.Parallel()
	d := vgpuDevice(t, newVgpuEngine(t, 1, vgpuGuestVirtualization(&VGPULicenseConfig{
		Product: "NVIDIA Virtual Compute Server", State: "licensed", Expiry: "2027-01-02T03:04:05Z",
	})), 0)

	f, ret := d.GetGridLicensableFeatures()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, int32(1), f.IsGridLicenseSupported)
	require.Equal(t, uint32(1), f.LicensableFeaturesCount)
	feat := f.GridLicensableFeatures[0]
	require.Equal(t, "NVIDIA Virtual Compute Server", cString(feat.ProductName[:]))
	require.Equal(t, uint32(1), feat.FeatureState)
	require.Equal(t, uint32(1), feat.FeatureEnabled)
	require.Equal(t, nvml.GridLicenseExpiry{
		Year: 2027, Month: 1, Day: 2, Hour: 3, Min: 4, Sec: 5, Status: nvml.GRID_LICENSE_EXPIRY_VALID,
	}, feat.LicenseExpiry)

	state, ok := d.VgpuGuestLicenseState()
	require.True(t, ok)
	require.Equal(t, uint32(nvml.GRID_LICENSE_STATE_LICENSED), state)
}

func TestVgpuGuest_PermanentWithoutExpiry(t *testing.T) {
	t.Parallel()
	d := vgpuDevice(t, newVgpuEngine(t, 1, vgpuGuestVirtualization(&VGPULicenseConfig{Product: "p"})), 0)
	f, ret := d.GetGridLicensableFeatures()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint8(nvml.GRID_LICENSE_EXPIRY_PERMANENT), f.GridLicensableFeatures[0].LicenseExpiry.Status)
}

func TestVgpuGuest_Unlicensed(t *testing.T) {
	t.Parallel()
	for name, license := range map[string]*VGPULicenseConfig{
		"no license block":   nil,
		"explicitly revoked": {Product: "p", State: "unlicensed"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := vgpuDevice(t, newVgpuEngine(t, 1, vgpuGuestVirtualization(license)), 0)
			f, ret := d.GetGridLicensableFeatures()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, uint32(0), f.GridLicensableFeatures[0].FeatureState)
			require.Equal(t, uint8(nvml.GRID_LICENSE_EXPIRY_NOT_APPLICABLE), f.GridLicensableFeatures[0].LicenseExpiry.Status)

			state, ok := d.VgpuGuestLicenseState()
			require.True(t, ok)
			require.Equal(t, uint32(nvml.GRID_LICENSE_STATE_UNLICENSED), state)
		})
	}
}

func TestVgpuGuest_NotAGuest(t *testing.T) {
	t.Parallel()
	for name, virt := range map[string]*VirtualizationConfig{
		"bare metal":           {Mode: "none"},
		"host":                 vgpuHostVirtualization(1),
		"vgpu without a block": {Mode: VirtualizationModeVGPU},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := vgpuDevice(t, newVgpuEngine(t, 1, virt), 0)
			_, ret := d.GetGridLicensableFeatures()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
			_, ok := d.VgpuGuestLicenseState()
			require.False(t, ok)
		})
	}
}
