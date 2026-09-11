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

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = MIGAddGpuInstance(path, 0, engine.MIGGPUInstanceRecord{
				ID: uint32(i), Profile: "1g.5gb",
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	mig := migDoc(t, path)
	require.Len(t, *mig.Instances, 8, "every concurrent write must survive")
}
