// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// SPDX-License-Identifier: Apache-2.0
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
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// migOffConfig is one MIG-capable board with MIG switched off, the state
// `nvidia-smi -mig 1` is pointed at.
const migOffConfig = `version: "1.0"
system:
  driver_version: "550.163.01"
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  architecture: "ampere"
  mig:
    mode_current: "disabled"
    mode_pending: "disabled"
    max_gpu_instances: 7
devices:
  - index: 0
    uuid: "GPU-12345678-1234-1234-1234-123456780000"
`

// migDeclaredConfig is the same board partitioned by its profile rather than
// at runtime, which is the layout a delta writer has to learn before it can
// edit it.
const migDeclaredConfig = `version: "1.0"
system:
  driver_version: "550.163.01"
device_defaults:
  name: "NVIDIA A100-SXM4-40GB"
  architecture: "ampere"
  mig:
    mode_current: "enabled"
    mode_pending: "enabled"
    max_gpu_instances: 7
    gpu_instances:
      - profile: "1g.5gb"
        count: 3
devices:
  - index: 0
    uuid: "GPU-12345678-1234-1234-1234-123456780000"
`

// bootMIGEngine starts the engine on the given config with its override
// document at overrides, and hands back the handle for device 0. The engine is
// a process-global singleton, so tests using it cannot run in parallel.
func bootMIGEngine(t *testing.T, config, overrides string) unsafe.Pointer {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	t.Setenv("MOCK_NVML_CONFIG", configPath)
	t.Setenv("MOCK_NVML_OVERRIDES", overrides)
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)
	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })

	handle, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	return handle
}

// recordedMIG reads back what the document says about device 0's MIG state,
// through the same merge the engine applies, so a test cannot pass on a block
// the engine would reject.
func recordedMIG(t *testing.T, overrides string) *engine.MIGConfig {
	t.Helper()
	doc, err := mockctl.Load(overrides)
	require.NoError(t, err)
	require.Contains(t, doc.Devices, "0", "no overrides recorded for device 0")
	cfg, err := engine.MergeDeviceConfig(&engine.DeviceConfig{}, doc.Devices["0"])
	require.NoError(t, err)
	require.NotNil(t, cfg.MIG, "no mig block recorded for device 0")
	return cfg.MIG
}

// recordedInstances is the explicit layout the document carries, which is what
// a later process rebuilds the board from.
func recordedInstances(t *testing.T, overrides string) []engine.MIGGPUInstanceRecord {
	t.Helper()
	mig := recordedMIG(t, overrides)
	require.NotNil(t, mig.Instances, "no explicit layout recorded")
	return *mig.Instances
}

func recordedIDs(records []engine.MIGGPUInstanceRecord) []uint32 {
	ids := make([]uint32, 0, len(records))
	for _, rec := range records {
		ids = append(ids, rec.ID)
	}
	return ids
}

// The whole point: every mutation NVML can make has to be in the document by
// the time the call returns, because the process that made it is about to
// exit and take its engine with it.
func TestMIGMutations_AreRecordedAsTheyHappen(t *testing.T) {
	overrides := filepath.Join(t.TempDir(), "overrides.yaml")
	device := bootMIGEngine(t, migOffConfig, overrides)

	ret, _ := migSetMode(device, nvml.DEVICE_MIG_ENABLE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "enabled", recordedMIG(t, overrides).ModeCurrent)

	gi, ret := migCreateGpuInstance(device, nvml.GPU_INSTANCE_PROFILE_1_SLICE, nil)
	require.Equal(t, nvml.SUCCESS, ret)
	giInfo, _, ret := engine.GetEngine().GpuInstanceGetInfo(gi)
	require.Equal(t, nvml.SUCCESS, ret)

	records := recordedInstances(t, overrides)
	require.Equal(t, []uint32{giInfo.Id}, recordedIDs(records))
	require.NotNil(t, records[0].ComputeInstances,
		"a created GPU instance has no compute instances, and the record has to say so")
	require.Empty(t, *records[0].ComputeInstances)

	ci, ret := migCreateComputeInstance(gi, nvml.COMPUTE_INSTANCE_PROFILE_1_SLICE, nil)
	require.Equal(t, nvml.SUCCESS, ret)
	ciInfo, _, _, ret := engine.GetEngine().ComputeInstanceGetInfo(ci)
	require.Equal(t, nvml.SUCCESS, ret)
	records = recordedInstances(t, overrides)
	require.Len(t, records, 1)
	require.Equal(t, []engine.MIGComputeInstanceRecord{{ID: ciInfo.Id, ProfileID: intPtr(0)}},
		*records[0].ComputeInstances)

	require.Equal(t, nvml.SUCCESS, migDestroyComputeInstance(ci))
	records = recordedInstances(t, overrides)
	require.Len(t, records, 1)
	require.Empty(t, *records[0].ComputeInstances)

	require.Equal(t, nvml.SUCCESS, migDestroyGpuInstance(gi))
	require.Empty(t, recordedInstances(t, overrides),
		"a board emptied at runtime is an empty layout, not an absent one")

	ret, _ = migSetMode(device, nvml.DEVICE_MIG_DISABLE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "disabled", recordedMIG(t, overrides).ModeCurrent)
}

// A board partitioned by its profile has no explicit layout in the document,
// so a destroy recorded as a bare difference would leave the survivors
// unrecorded and a later process would rebuild all three.
func TestMIGMutations_DestroyOnADeclaredBoardKeepsTheSurvivors(t *testing.T) {
	overrides := filepath.Join(t.TempDir(), "overrides.yaml")
	device := bootMIGEngine(t, migDeclaredConfig, overrides)

	e := engine.GetEngine()
	dev := e.LookupConfigurableDevice(device)
	require.NotNil(t, dev)
	before := dev.MIGLayoutRecords()
	require.Len(t, before, 3, "the profile declares three instances")

	handles, ret := e.DeviceGetGpuInstances(device, nvml.GPU_INSTANCE_PROFILE_1_SLICE)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, handles, 3)
	require.Equal(t, nvml.SUCCESS, migDestroyGpuInstance(handles[1]))

	require.Equal(t, []uint32{before[0].ID, before[2].ID}, recordedIDs(recordedInstances(t, overrides)),
		"the survivors have to be recorded, not just the instance that went away")
}

// With nowhere to record it, a mutation has to be refused rather than applied
// in this process alone: the engine assigns the instance id, so a mutation
// that has already happened cannot be taken back.
func TestMIGMutations_RefusedWhenThereIsNowhereToRecord(t *testing.T) {
	t.Setenv("MOCK_NVML_NUM_DEVICES", "1")
	t.Setenv("MOCK_NVML_CONFIG", "")
	t.Setenv("MOCK_NVML_OVERRIDES", "")
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)
	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	require.Empty(t, engine.ConfigOverridePath(), "this test needs a mock with no override document")

	device, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	modeRet, _ := migSetMode(device, nvml.DEVICE_MIG_ENABLE)
	require.Equal(t, nvml.ERROR_NO_PERMISSION, modeRet)

	dev := e.LookupConfigurableDevice(device)
	require.NotNil(t, dev)
	mode, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.DEVICE_MIG_DISABLE, mode, "a refused mutation must not have been applied")
}

func intPtr(v int) *int { return &v }
