// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// vGPU guest licensing: what a VM's driver reports about the vGPU software
// license. nvidia-smi -q reads the product and expiry from
// GetGridLicensableFeatures, and the License Status line from an export-table
// slot the bridge answers through VgpuGuestLicenseState.

// GetGridLicensableFeatures overrides the embedded mock. A guest reports one
// licensable feature: the vGPU product its config names.
func (d *ConfigurableDevice) GetGridLicensableFeatures() (nvml.GridLicensableFeatures, nvml.Return) {
	if ret := d.handleLookupReturn(); ret != nvml.SUCCESS {
		return nvml.GridLicensableFeatures{}, ret
	}
	g := d.cfg().Virtualization.Guest()
	if g == nil {
		return nvml.GridLicensableFeatures{}, nvml.ERROR_NOT_SUPPORTED
	}
	feature := nvml.GridLicensableFeature{
		FeatureCode:   uint32(nvml.GRID_LICENSE_FEATURE_CODE_VGPU),
		LicenseExpiry: nvml.GridLicenseExpiry{Status: nvml.GRID_LICENSE_EXPIRY_NOT_APPLICABLE},
	}
	if l := g.License; l != nil {
		writeBusID(feature.ProductName[:], l.Product)
		if l.Licensed() {
			feature.FeatureState, feature.FeatureEnabled = 1, 1
			feature.LicenseExpiry = gridLicenseExpiry(l.Expiry)
		}
	}
	out := nvml.GridLicensableFeatures{IsGridLicenseSupported: 1, LicensableFeaturesCount: 1}
	out.GridLicensableFeatures[0] = feature
	debugLog("[NVML] nvmlDeviceGetGridLicensableFeatures -> licensed=%d\n", feature.FeatureState)
	return out, nvml.SUCCESS
}

// gridLicenseExpiry converts a validated RFC 3339 expiry. An absent one is a
// permanent license.
func gridLicenseExpiry(expiry string) nvml.GridLicenseExpiry {
	if expiry == "" {
		return nvml.GridLicenseExpiry{Status: nvml.GRID_LICENSE_EXPIRY_PERMANENT}
	}
	t, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		// Load-time validation rejects this; a config that bypassed it
		// reports an expiry nvidia-smi renders as invalid.
		warnLog("vGPU license expiry %q is not RFC 3339: %v\n", expiry, err)
		return nvml.GridLicenseExpiry{Status: nvml.GRID_LICENSE_EXPIRY_INVALID}
	}
	t = t.UTC()
	return nvml.GridLicenseExpiry{
		Year:   uint32(t.Year()),
		Month:  uint16(t.Month()),
		Day:    uint16(t.Day()),
		Hour:   uint16(t.Hour()),
		Min:    uint16(t.Minute()),
		Sec:    uint16(t.Second()),
		Status: nvml.GRID_LICENSE_EXPIRY_VALID,
	}
}

// VgpuGuestLicenseState reports the guest's license state in
// nvmlGridLicenseState_t terms, and false for a GPU that is not a vGPU guest.
func (d *ConfigurableDevice) VgpuGuestLicenseState() (uint32, bool) {
	g := d.cfg().Virtualization.Guest()
	if g == nil {
		return 0, false
	}
	if g.License.Licensed() {
		return nvml.GRID_LICENSE_STATE_LICENSED, true
	}
	return nvml.GRID_LICENSE_STATE_UNLICENSED, true
}
