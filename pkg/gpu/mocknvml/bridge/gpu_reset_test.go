// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeOverrides seeds an overrides file and returns its path.
func writeOverrides(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestClearDeviceOverrides_RemovesTheDeviceBucket(t *testing.T) {
	path := writeOverrides(t, `version: 1
devices:
  "1":
    thermal:
      temperature_gpu_c: 99
`)

	require.NoError(t, clearDeviceOverrides(path, 1))

	has, err := hasDeviceOverrides(path, 1)
	require.NoError(t, err)
	require.False(t, has, "GPU 1's overrides should be gone after the reset")
}

// A reset is scoped to the GPU nvidia-smi targeted with -i, so a sibling's
// injected state has to survive it.
func TestClearDeviceOverrides_LeavesOtherDevicesAlone(t *testing.T) {
	path := writeOverrides(t, `version: 1
devices:
  "0":
    thermal:
      temperature_gpu_c: 91
  "1":
    thermal:
      temperature_gpu_c: 99
`)

	require.NoError(t, clearDeviceOverrides(path, 1))

	has, err := hasDeviceOverrides(path, 0)
	require.NoError(t, err)
	require.True(t, has, "GPU 0 must keep its overrides when only GPU 1 was reset")
}

// Resetting a healthy GPU must not rewrite the file. That is the common case,
// and it is what keeps a reset working where the overrides are not writable.
func TestClearDeviceOverrides_HealthyDeviceLeavesFileUntouched(t *testing.T) {
	body := `version: 1
devices:
  "0":
    thermal:
      temperature_gpu_c: 91
`
	path := writeOverrides(t, body)

	require.NoError(t, clearDeviceOverrides(path, 3))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, string(after), "a reset with nothing to clear must not rewrite the file")
	require.NoFileExists(t, path+".lock", "a reset with nothing to clear must not even take the lock")
}

// No overrides file at all means nothing was ever injected: the device is
// already at its baseline, so the reset succeeds without creating a file.
func TestClearDeviceOverrides_MissingFileSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")

	require.NoError(t, clearDeviceOverrides(path, 0))
	require.NoFileExists(t, path)
}

// With no config there is no override file to resolve, and nothing to clear.
func TestClearDeviceOverrides_EmptyPathSucceeds(t *testing.T) {
	require.NoError(t, clearDeviceOverrides("", 0))
}

// A per-device reset leaves the shared `all:` bucket in place, matching
// `nvml-mock-ctl reset --gpu <n>` exactly — the override schema cannot express
// "drop the shared block for one GPU only". An operator who injected with
// `--gpu all` needs `nvml-mock-ctl reset --gpu all`; nvidia-smi will report a
// successful reset while the shared block still masks the device.
func TestClearDeviceOverrides_LeavesSharedAllBucket(t *testing.T) {
	body := `version: 1
all:
  thermal:
    temperature_gpu_c: 88
`
	path := writeOverrides(t, body)

	require.NoError(t, clearDeviceOverrides(path, 0))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, string(after))
}

// A corrupt overrides file must fail the reset rather than be silently treated
// as "nothing injected", which would report success over unknown device state.
func TestClearDeviceOverrides_ReportsUnparseableFile(t *testing.T) {
	path := writeOverrides(t, "devices: [this is not a mapping\n")

	require.Error(t, clearDeviceOverrides(path, 0))
}
