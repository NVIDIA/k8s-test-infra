// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A profile that declares no driver: block must report what the nvidia module
// itself reports, since that is what the agent compiles in that case.
func TestDeviceFileParamsDefaultToDriverBehaviour(t *testing.T) {
	dir := t.TempDir()
	yaml := `
device_defaults:
  name: "NVIDIA TEST-GPU"
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.yaml"), []byte(yaml), 0o600))

	p, err := Load(dir, "fixture")
	require.NoError(t, err, "Load(fixture)")

	require.Equal(t, DeviceFileParams{Mode: 0o666, ModifyDeviceFiles: true}, p.DeviceFileParams())
}

// The values have to come from the profile, or the e2e check would pass against
// a surface that ignores the driver: block entirely.
func TestDeviceFileParamsFromProfile(t *testing.T) {
	dir := t.TempDir()
	yaml := `
device_defaults:
  name: "NVIDIA TEST-GPU"
driver:
  device_file_uid: 1000
  device_file_gid: 27
  device_file_mode: 0660
  modify_device_files: false
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.yaml"), []byte(yaml), 0o600))

	p, err := Load(dir, "fixture")
	require.NoError(t, err, "Load(fixture)")

	require.Equal(t,
		DeviceFileParams{UID: 1000, GID: 27, Mode: 0o660, ModifyDeviceFiles: false},
		p.DeviceFileParams())
}

// device_file_mode is written as an octal literal in the profiles, and YAML
// decodes 0660 as octal 0o660 rather than decimal 660. Pinned because the
// params file reports the decimal form, so a misread here would compare 660
// against 432 and look like a rendering bug.
func TestDeviceFileModeIsOctal(t *testing.T) {
	dir := t.TempDir()
	yaml := `
device_defaults:
  name: "NVIDIA TEST-GPU"
driver:
  device_file_mode: 0660
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.yaml"), []byte(yaml), 0o600))

	p, err := Load(dir, "fixture")
	require.NoError(t, err, "Load(fixture)")

	require.Equal(t, 0o660, p.DeviceFileParams().Mode, "0660 must decode as octal")
}

// Every shipped profile leaves the block out today. Asserted so that adding
// one is a deliberate act that shows up here rather than silently changing
// what the e2e run expects on that profile.
func TestShippedProfilesUseTheDriverDefaults(t *testing.T) {
	ps, err := All(profilesDir)
	require.NoError(t, err, "All()")

	for _, p := range ps {
		require.Equal(t, DeviceFileParams{Mode: 0o666, ModifyDeviceFiles: true}, p.DeviceFileParams(),
			"profile %s declares a driver: block; update the e2e expectation deliberately", p.Name)
	}
}
