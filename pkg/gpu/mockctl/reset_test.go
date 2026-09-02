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

package mockctl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeOverridesFile seeds an overrides file and returns its path.
func writeOverridesFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestResetDevice_RemovesTheDeviceBucket(t *testing.T) {
	path := writeOverridesFile(t, `version: 1
devices:
  "1":
    thermal:
      temperature_gpu_c: 99
`)

	require.NoError(t, ResetDevice(path, 1))

	has, err := deviceHasOverrides(path, 1)
	require.NoError(t, err)
	require.False(t, has, "GPU 1's overrides should be gone after the reset")
}

// A reset is scoped to the GPU nvidia-smi targeted with -i, so a sibling's
// injected state has to survive it.
func TestResetDevice_LeavesOtherDevicesAlone(t *testing.T) {
	path := writeOverridesFile(t, `version: 1
devices:
  "0":
    thermal:
      temperature_gpu_c: 91
  "1":
    thermal:
      temperature_gpu_c: 99
`)

	require.NoError(t, ResetDevice(path, 1))

	has, err := deviceHasOverrides(path, 0)
	require.NoError(t, err)
	require.True(t, has, "GPU 0 must keep its overrides when only GPU 1 was reset")
}

// Resetting a healthy GPU must not rewrite the file. That is the common case,
// and it is what keeps a reset working where the overrides are not writable.
func TestResetDevice_HealthyDeviceLeavesFileUntouched(t *testing.T) {
	body := `version: 1
devices:
  "0":
    thermal:
      temperature_gpu_c: 91
`
	path := writeOverridesFile(t, body)

	require.NoError(t, ResetDevice(path, 3))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, string(after), "a reset with nothing to clear must not rewrite the file")
	require.NoFileExists(t, path+".lock", "a reset with nothing to clear must not even take the lock")
}

// No overrides file at all means nothing was ever injected: the device is
// already at its baseline, so the reset succeeds without creating a file.
func TestResetDevice_MissingFileSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")

	require.NoError(t, ResetDevice(path, 0))
	require.NoFileExists(t, path)
}

// With no config there is no override file to resolve, and nothing to clear.
func TestResetDevice_EmptyPathSucceeds(t *testing.T) {
	require.NoError(t, ResetDevice("", 0))
}

// A per-device reset leaves the shared `all:` bucket in place, matching
// `nvml-mock-ctl reset --gpu <n>` exactly — the override schema cannot express
// "drop the shared block for one GPU only". An operator who injected with
// `--gpu all` needs a Target{All: true} reset; nvidia-smi will report a
// successful reset while the shared block still masks the device.
func TestResetDevice_LeavesSharedAllBucket(t *testing.T) {
	// The device bucket is what makes this test load-bearing: without it the
	// reset stops at the "nothing injected" early return, never reaches
	// Doc.Reset, and passes on a file it never opened for writing.
	path := writeOverridesFile(t, `version: 1
all:
  thermal:
    temperature_gpu_c: 88
devices:
  "0":
    thermal:
      temperature_gpu_c: 99
`)

	require.NoError(t, ResetDevice(path, 0))

	doc, err := Load(path)
	require.NoError(t, err)
	require.NotContains(t, doc.Devices, "0", "the reset must clear the device's own bucket")
	require.Equal(t, map[string]any{"thermal": map[string]any{"temperature_gpu_c": float64(88)}}, doc.All,
		"the shared block survives a per-device reset, so the device stays masked by it")
}

// A corrupt overrides file must fail the reset rather than be silently treated
// as "nothing injected", which would report success over unknown device state.
func TestResetDevice_ReportsUnparseableFile(t *testing.T) {
	path := writeOverridesFile(t, "devices: [this is not a mapping\n")

	require.Error(t, ResetDevice(path, 0))
}
