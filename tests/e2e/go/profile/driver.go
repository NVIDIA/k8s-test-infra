// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

// rawDriver decodes the profile's driver: block, the nvidia module's
// device-node parameters. Pointers distinguish an absent key from a deliberate
// zero: uid 0 and modify_device_files: false are both meaningful values.
type rawDriver struct {
	DeviceFileUID     *int  `json:"device_file_uid"`
	DeviceFileGID     *int  `json:"device_file_gid"`
	DeviceFileMode    *int  `json:"device_file_mode"`
	ModifyDeviceFiles *bool `json:"modify_device_files"`
}

// DeviceFileParams is the device-node ownership and permissions the profile
// configures, as reported through /proc/driver/nvidia/params. Mode is the
// numeric mode (0o666), which the params file reports in its decimal form.
type DeviceFileParams struct {
	UID               int
	GID               int
	Mode              int
	ModifyDeviceFiles bool
}

// driverDefaults mirrors internal/agent/source.compileDriverParams: what the
// real nvidia module reports when the profile says nothing. Kept in step with
// the agent by TestShippedProfilesUseTheDriverDefaults, which fails if a
// profile starts declaring the block.
func driverDefaults() DeviceFileParams {
	return DeviceFileParams{Mode: 0o666, ModifyDeviceFiles: true}
}

// resolveDeviceFileParams applies the profile's driver: block over the driver's
// own defaults, key by key, the way the agent compiles it.
func resolveDeviceFileParams(raw *rawDriver) DeviceFileParams {
	params := driverDefaults()
	if raw == nil {
		return params
	}

	if raw.DeviceFileUID != nil {
		params.UID = *raw.DeviceFileUID
	}
	if raw.DeviceFileGID != nil {
		params.GID = *raw.DeviceFileGID
	}
	if raw.DeviceFileMode != nil {
		params.Mode = *raw.DeviceFileMode
	}
	if raw.ModifyDeviceFiles != nil {
		params.ModifyDeviceFiles = *raw.ModifyDeviceFiles
	}

	return params
}

// DeviceFileParams returns the device-node parameters the profile configures,
// or the driver's defaults where it configures none.
func (p Profile) DeviceFileParams() DeviceFileParams { return p.deviceFileParams }
