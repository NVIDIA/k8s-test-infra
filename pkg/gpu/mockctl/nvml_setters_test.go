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
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func overridePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "overrides.yaml")
}

// TestSetPowerLimit_SurvivesReload is the whole point of the change: the write
// has to land in the shared document, not in the writer's memory, so a reader
// that never saw the writing process still observes it.
func TestSetPowerLimit_SurvivesReload(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	require.NoError(t, SetPowerLimit(path, 0, 400_000))

	doc, err := Load(path)
	require.NoError(t, err)
	power := doc.Devices["0"]["power"].(map[string]any)
	require.EqualValues(t, 400_000, power["enforced_limit_mw"])
}

func TestSetPowerLimit_FailsWithoutADocumentPath(t *testing.T) {
	t.Parallel()
	require.Error(t, SetPowerLimit("", 0, 400_000),
		"a setter that cannot persist must report failure rather than silently succeed")
}

func TestSetNvlinkBwMode_SurvivesReload(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	require.NoError(t, SetNvlinkBwMode(path, 1, 3, false))

	doc, err := Load(path)
	require.NoError(t, err)
	require.EqualValues(t, 3, doc.Devices["1"]["nvlink_bw_mode"])
	require.NotContains(t, doc.All, "nvlink_bw_mode",
		"a per-device set must not land in the bucket the node-wide getter reads")
}

// TestSetNvlinkBwMode_NodeWideTargetsAllBucket pins where the node-wide NVML
// pair records its value. It has to be the `all:` bucket: the getter takes no
// device, and every device must move with it.
func TestSetNvlinkBwMode_NodeWideTargetsAllBucket(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	require.NoError(t, SetNvlinkBwMode(path, 0, 2, true))

	doc, err := Load(path)
	require.NoError(t, err)
	require.EqualValues(t, 2, doc.All["nvlink_bw_mode"])
	require.Empty(t, doc.Devices, "a node-wide set belongs to no single device")
}

// TestSetNvlinkLowPowerThreshold_ClearsOnReset pins that the reset sentinel
// removes the field rather than recording a zero, so the device falls back to
// the driver default instead of to a threshold nobody asked for.
func TestSetNvlinkLowPowerThreshold_ClearsOnReset(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	threshold := uint32(500)
	require.NoError(t, SetNvlinkLowPowerThreshold(path, 0, &threshold))

	doc, err := Load(path)
	require.NoError(t, err)
	require.EqualValues(t, 500, doc.Devices["0"]["nvlink_low_power_threshold"])

	require.NoError(t, SetNvlinkLowPowerThreshold(path, 0, nil))

	doc, err = Load(path)
	require.NoError(t, err)
	require.NotContains(t, doc.Devices["0"], "nvlink_low_power_threshold",
		"a reset must remove the recorded threshold")
}

func TestNvlinkSetters_FailWithoutADocumentPath(t *testing.T) {
	t.Parallel()
	require.Error(t, SetNvlinkBwMode("", 0, 3, false), "bandwidth mode")
	require.Error(t, SetNvlinkLowPowerThreshold("", 0, nil), "low-power threshold")
}

// TestUpdateWorkloadProfiles_ReportsNoBaseUntilSomethingIsWritten lets the
// caller tell "nobody has written" from "written, then cleared": only the
// former may fall back to the profile's configured request.
func TestUpdateWorkloadProfiles_ReportsNoBaseUntilSomethingIsWritten(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	var gotPresent bool
	var gotBase []uint32
	require.NoError(t, UpdateWorkloadProfiles(path, 0,
		func(base []uint32, present bool) ([]uint32, error) {
			gotBase, gotPresent = base, present
			return []uint32{6}, nil
		}))
	require.False(t, gotPresent, "an untouched document carries no request")
	require.Nil(t, gotBase)

	require.NoError(t, UpdateWorkloadProfiles(path, 0,
		func(base []uint32, present bool) ([]uint32, error) {
			gotBase, gotPresent = base, present
			return base, nil
		}))
	require.True(t, gotPresent, "the previous write should be visible as the base")
	require.Equal(t, []uint32{6}, gotBase)
}

// TestUpdateWorkloadProfiles_ClearedListIsNotAbsent guards the distinction the
// merge relies on: an empty request must round-trip as an empty list rather
// than as null, which would read back as "nothing was ever written".
func TestUpdateWorkloadProfiles_ClearedListIsNotAbsent(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	require.NoError(t, UpdateWorkloadProfiles(path, 0,
		func([]uint32, bool) ([]uint32, error) { return []uint32{6}, nil }))
	require.NoError(t, UpdateWorkloadProfiles(path, 0,
		func([]uint32, bool) ([]uint32, error) { return nil, nil }))

	var gotPresent bool
	var gotBase []uint32
	require.NoError(t, UpdateWorkloadProfiles(path, 0,
		func(base []uint32, present bool) ([]uint32, error) {
			gotBase, gotPresent = base, present
			return base, nil
		}))
	require.True(t, gotPresent, "a cleared request is still a request that was written")
	require.Empty(t, gotBase)
}

func TestUpdateWorkloadProfiles_RejectedUpdateLeavesTheFileAlone(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	require.NoError(t, UpdateWorkloadProfiles(path, 0,
		func([]uint32, bool) ([]uint32, error) { return []uint32{6}, nil }))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Error(t, UpdateWorkloadProfiles(path, 0,
		func([]uint32, bool) ([]uint32, error) { return nil, errors.New("unsupported profile") }))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "a refused update must not rewrite the document")
}

// TestUpdateWorkloadProfiles_ConcurrentWritersDoNotLoseUpdates is why the write
// takes the flock: the update is a read-modify-write, so two unsynchronised
// callers would each compute their result from the same base and one would win.
func TestUpdateWorkloadProfiles_ConcurrentWritersDoNotLoseUpdates(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	ids := []uint32{0, 1, 5, 6, 13}
	errs := make([]error, len(ids))

	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = UpdateWorkloadProfiles(path, 0,
				func(base []uint32, _ bool) ([]uint32, error) {
					return append(append([]uint32{}, base...), id), nil
				})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "writer %d", i)
	}

	var got []uint32
	require.NoError(t, UpdateWorkloadProfiles(path, 0,
		func(base []uint32, _ bool) ([]uint32, error) {
			got = base
			return base, nil
		}))
	require.ElementsMatch(t, ids, got, "a missing id means one writer's update was lost")
}

// TestSetters_DoNotDisturbOtherDevices keeps a per-device write from behaving
// like the node-wide one; nvidia-smi caps one GPU at a time.
func TestSetters_DoNotDisturbOtherDevices(t *testing.T) {
	t.Parallel()
	path := overridePath(t)

	require.NoError(t, SetPowerLimit(path, 0, 400_000))
	require.NoError(t, SetPowerLimit(path, 1, 300_000))

	doc, err := Load(path)
	require.NoError(t, err)
	require.EqualValues(t, 400_000, doc.Devices["0"]["power"].(map[string]any)["enforced_limit_mw"])
	require.EqualValues(t, 300_000, doc.Devices["1"]["power"].(map[string]any)["enforced_limit_mw"])
}
