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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// migDoc reads back what a writer recorded the way the engine will: through the
// same merge, so a test cannot pass on a block the engine would reject.
func migDoc(t *testing.T, path string) *engine.MIGConfig {
	t.Helper()
	doc, err := Load(path)
	require.NoError(t, err)
	cfg, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, doc.Devices["0"])
	require.NoError(t, err)
	return cfg.MIG
}

func TestMIGSetMode_WritesBothModeFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")

	require.NoError(t, MIGSetMode(path, 0, true))

	mig := migDoc(t, path)
	require.NotNil(t, mig)
	require.Equal(t, "enabled", mig.ModeCurrent)
	require.Equal(t, "enabled", mig.ModePending)
}

// Disabling MIG destroys every instance on hardware, so the recorded layout
// has to go with it rather than lying in wait for the next enable.
func TestMIGSetMode_DisableClearsTheLayout(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")

	require.NoError(t, MIGSetMode(path, 0, true))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 0, Profile: "1g.5gb"}))
	require.NoError(t, MIGSetMode(path, 0, false))

	mig := migDoc(t, path)
	require.Equal(t, "disabled", mig.ModeCurrent)
	require.Nil(t, mig.Instances)
}

func TestMIGAddAndRemoveGpuInstance(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))

	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 0, Profile: "1g.5gb"}))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 1, Profile: "1g.5gb"}))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 2, Profile: "1g.5gb"}))
	require.NoError(t, MIGRemoveGpuInstance(path, 0, 1))

	mig := migDoc(t, path)
	require.NotNil(t, mig.Instances)
	ids := make([]uint32, 0, len(*mig.Instances))
	for _, gi := range *mig.Instances {
		ids = append(ids, gi.ID)
	}
	require.Equal(t, []uint32{0, 2}, ids, "the survivors keep their ids")
}

// Removing the last instance leaves an empty list, not an absent key: the
// board is MIG-enabled with nothing on it, and an absent key would read as
// "no explicit layout" and fall back to the profile's partitions.
func TestMIGRemoveGpuInstance_LastOneLeavesAnEmptyList(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 0, Profile: "1g.5gb"}))

	require.NoError(t, MIGRemoveGpuInstance(path, 0, 0))

	mig := migDoc(t, path)
	require.NotNil(t, mig.Instances, "an empty layout must still be present")
	require.Empty(t, *mig.Instances)
}

func TestMIGComputeInstances(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 3, Profile: "1g.5gb"}))

	require.NoError(t, MIGAddComputeInstance(path, 0, 3, engine.MIGComputeInstanceRecord{ID: 0, Profile: "1c"}))
	require.NoError(t, MIGAddComputeInstance(path, 0, 3, engine.MIGComputeInstanceRecord{ID: 1, Profile: "1c"}))
	require.NoError(t, MIGRemoveComputeInstance(path, 0, 3, 0))

	mig := migDoc(t, path)
	require.Len(t, *mig.Instances, 1)
	cis := (*mig.Instances)[0].ComputeInstances
	require.NotNil(t, cis)
	require.Len(t, *cis, 1)
	require.Equal(t, uint32(1), (*cis)[0].ID)
}

// A GPU instance created without compute instances must round-trip with none.
// `nvidia-smi mig -cgi` without `-C` produces exactly this, and an absent list
// would hand the instance back a spanning compute instance it never had.
func TestMIGAddGpuInstance_RecordsNoComputeInstancesAsEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))

	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{
		ID: 0, Profile: "1g.5gb", ComputeInstances: &[]engine.MIGComputeInstanceRecord{},
	}))

	mig := migDoc(t, path)
	cis := (*mig.Instances)[0].ComputeInstances
	require.NotNil(t, cis, "an instance with no compute instances must say so")
	require.Empty(t, *cis)
}

// A mutation owns the mode and the explicit layout; every other key in the
// block belongs to whoever wrote it. The explicit zero is the sharp case: the
// engine deep-merges this block over the profile, so a key dropped on the way
// back out reads as "inherit the profile's value" rather than as zero.
func TestMIGAddGpuInstance_LeavesTheRestOfTheBlockAlone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	doc, err := Load(path)
	require.NoError(t, err)
	doc.SetMIG(Target{Index: 0}, map[string]any{
		"mode_current":      "enabled",
		"mode_pending":      "enabled",
		"max_gpu_instances": 0,
		"gpu_instances":     []any{map[string]any{"profile": "1g.5gb", "count": 7}},
	})
	require.NoError(t, WriteAtomic(path, doc))

	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 0, Profile: "1g.5gb"}))

	doc, err = Load(path)
	require.NoError(t, err)
	block, ok := doc.Devices["0"]["mig"].(map[string]any)
	require.True(t, ok, "the mig block must stay a generic map")
	require.Contains(t, block, "instances")
	require.Contains(t, block, "max_gpu_instances", "an explicit zero must survive a mutation")
	require.EqualValues(t, 0, block["max_gpu_instances"])
	require.Contains(t, block, "gpu_instances", "the declared counts are not a mutation's to rewrite")
}

// Two writers racing must both land: this is the whole reason the mutation
// re-reads the document under the lock instead of writing a view it loaded
// earlier.
func TestMIGAddGpuInstance_ConcurrentWritersBothLand(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))

	// Released together rather than as they are spawned, so the writers really
	// do contend instead of happening to serialize behind the loop. The gate is
	// only closed once every writer is parked on it: closing it earlier would
	// let a late-starting goroutine sail through without ever blocking.
	start := make(chan struct{})
	var ready, wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		ready.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			errs[i] = MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{
				ID: uint32(i), Profile: "1g.5gb",
			})
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	mig := migDoc(t, path)
	require.Len(t, *mig.Instances, 8, "every concurrent write must survive")
}

// NVML allocates the IDs, so a duplicate is a replayed or mis-sequenced
// mutation rather than a second instance. Appending it would leave two records
// under one ID, and a later removal would take both.
func TestMIGAddGpuInstance_RejectsARecordedID(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 1, Profile: "1g.5gb"}))

	err := MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 1, Profile: "2g.10gb"})
	require.ErrorContains(t, err, "gpu instance 1 is already recorded")

	mig := migDoc(t, path)
	require.Len(t, *mig.Instances, 1, "the rejected record must not have landed")
	require.Equal(t, "1g.5gb", (*mig.Instances)[0].Profile)
}

func TestMIGAddComputeInstance_RejectsARecordedID(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 1, Profile: "1g.5gb"}))
	require.NoError(t, MIGAddComputeInstance(path, 0, 1, engine.MIGComputeInstanceRecord{ID: 0, Profile: "1c"}))

	err := MIGAddComputeInstance(path, 0, 1, engine.MIGComputeInstanceRecord{ID: 0, Profile: "1c"})
	require.ErrorContains(t, err, "compute instance 0 is already recorded in gpu instance 1")

	mig := migDoc(t, path)
	require.Len(t, *(*mig.Instances)[0].ComputeInstances, 1)
}

// A compute instance the layout has nowhere to put must fail rather than
// report success: the caller has already created it in its own process, and a
// silently dropped record would vanish at the next process start.
func TestMIGAddComputeInstance_RejectsAnUnrecordedGpuInstance(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))
	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 1, Profile: "1g.5gb"}))

	err := MIGAddComputeInstance(path, 0, 7, engine.MIGComputeInstanceRecord{ID: 0, Profile: "1c"})
	require.ErrorContains(t, err, "gpu instance 7 is not recorded")

	mig := migDoc(t, path)
	require.Len(t, *mig.Instances, 1, "a rejected mutation must leave no partial edit")
	require.Nil(t, (*mig.Instances)[0].ComputeInstances)
}

func TestMIGRemoveComputeInstance_RejectsAnUnrecordedGpuInstance(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))

	err := MIGRemoveComputeInstance(path, 0, 7, 0)
	require.ErrorContains(t, err, "gpu instance 7 is not recorded")
}

// A rejected mutation must not touch the document at all — not even to create
// the device's bucket, which would leave an empty `mig:` block behind for a
// device that had no overrides.
func TestMIGAddComputeInstance_RejectionWritesNothing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")

	err := MIGAddComputeInstance(path, 0, 0, engine.MIGComputeInstanceRecord{ID: 0, Profile: "1c"})
	require.ErrorContains(t, err, "gpu instance 0 is not recorded")

	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, os.ErrNotExist, "a rejected mutation must not create the document")
}

// The engine watches this file, so a mutation that changes nothing must leave
// it alone. A comment cannot survive a rewrite, which is what makes the
// difference between "not written" and "rewritten identically" observable.
func TestMIGSetMode_NoChangeDoesNotRewriteTheFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	marked := append([]byte("# written by hand\n"), written...)
	require.NoError(t, os.WriteFile(path, marked, 0o644))

	require.NoError(t, MIGSetMode(path, 0, true))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, marked, after, "a mutation that changes nothing must not rewrite the file")

	require.NoError(t, MIGSetMode(path, 0, false))
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NotEqual(t, marked, after, "a real change must still be written")
}

// The `instances` list round-trips through the typed records on every write,
// including writes that do not touch the layout, so a zero a record carries
// has to survive that trip. It does because every field whose zero the engine
// has to tell from absent is either non-omitempty or a pointer; an omitempty
// scalar added to either record type would not survive. Both record types are
// seeded, since either one could grow such a field.
func TestMIGAddGpuInstance_ZeroesInsideARecordSurvive(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	doc, err := Load(path)
	require.NoError(t, err)
	doc.SetMIG(Target{Index: 0}, map[string]any{
		"mode_current": "enabled",
		"mode_pending": "enabled",
		"instances": []any{map[string]any{
			"id": 0, "profile": "1g.5gb", "placement_start": 0,
			"compute_instances": []any{map[string]any{
				"id": 0, "profile": "1c", "profile_id": 0,
			}},
		}},
	})
	require.NoError(t, WriteAtomic(path, doc))

	require.NoError(t, MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{ID: 1, Profile: "1g.5gb"}))

	// Asserted against the raw document: merging cannot tell a zero from an
	// absent key, which is the exact loss at issue.
	doc, err = Load(path)
	require.NoError(t, err)
	block, ok := doc.Devices["0"]["mig"].(map[string]any)
	require.True(t, ok, "the mig block must stay a generic map")
	instances, ok := block["instances"].([]any)
	require.True(t, ok)
	require.Len(t, instances, 2)
	seeded, ok := instances[0].(map[string]any)
	require.True(t, ok)
	require.Contains(t, seeded, "id", "an instance id of zero must survive a mutation")
	require.EqualValues(t, 0, seeded["id"])
	require.Contains(t, seeded, "placement_start", "a placement of zero must survive a mutation")
	require.EqualValues(t, 0, seeded["placement_start"])

	cis, ok := seeded["compute_instances"].([]any)
	require.True(t, ok)
	require.Len(t, cis, 1)
	ci, ok := cis[0].(map[string]any)
	require.True(t, ok)
	require.Contains(t, ci, "id", "a compute instance id of zero must survive a mutation")
	require.EqualValues(t, 0, ci["id"])
	require.Contains(t, ci, "profile_id", "a compute profile id of zero must survive a mutation")
	require.EqualValues(t, 0, ci["profile_id"])
}

// Removing an instance from a device whose layout was never recorded cannot be
// expressed as a delta: absent means the profile's declared counts stand, so
// the remainder is unknowable and writing it as an empty list would record the
// destruction of every instance on the board.
func TestMIGRemoveGpuInstance_RejectsAnUnrecordedLayout(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	require.NoError(t, MIGSetMode(path, 0, true))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	err = MIGRemoveGpuInstance(path, 0, 1)
	require.ErrorContains(t, err, "gpu instance 1 is not recorded")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "a rejected removal must not rewrite the document")
	require.Nil(t, migDoc(t, path).Instances, "the declared counts must still stand")
}
