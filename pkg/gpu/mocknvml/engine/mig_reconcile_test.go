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

package engine

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
)

// A test here that installs its own config override store — newTestDevice does
// it, and a few do it inline — replaces a package global, so no such test may
// run in parallel. A test that installs none is free to.

// a100PartitionedConfig is an A100 whose profile already declares seven
// partitions and has MIG on — the state a MIG-enabled Helm install boots.
func a100PartitionedConfig() *DeviceConfig {
	cfg := a100MIGConfig()
	cfg.MIG.ModeCurrent = "enabled"
	cfg.MIG.ModePending = "enabled"
	cfg.MIG.GPUInstances = []MIGGPUInstanceConfig{{Profile: "1g.5gb", Count: 7}}
	return cfg
}

const migEnable7x1g = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      gpu_instances:
        - profile: 1g.5gb
          count: 7
`

const migEnable2x3g = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      gpu_instances:
        - profile: 3g.20gb
          count: 2
`

// migEnablePendingOnly is how hardware stages a MIG enable: the mode a reset
// would come up in flips before the current one does.
const migEnablePendingOnly = `devices:
  "0":
    mig:
      mode_current: disabled
      mode_pending: enabled
`

const migDisable = `devices:
  "0":
    mig:
      mode_current: disabled
      mode_pending: disabled
`

const migEnableMax3 = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      max_gpu_instances: 3
`

// The explicit documents below are the form a runtime mutation records: named
// instances rather than a count. migExplicitGap is migExplicitThree with the
// middle instance deleted, the layout a count cannot express.
const migExplicitTwo = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 1g.5gb
          placement_start: 0
        - id: 1
          profile: 1g.5gb
          placement_start: 1
`

const migExplicitThree = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 1g.5gb
          placement_start: 0
        - id: 1
          profile: 1g.5gb
          placement_start: 1
        - id: 2
          profile: 1g.5gb
          placement_start: 2
`

const migExplicitGap = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 1g.5gb
          placement_start: 0
        - id: 2
          profile: 1g.5gb
          placement_start: 2
`

// The pair below records one instance under two profiles at the same ID and
// offset. Hardware cannot resize an instance in place, so the second document
// is a replacement of instance 0 rather than an edit of it. A single instance
// keeps the wider profile placeable, which it would not be beside a neighbour.
const migExplicitOneSmallInstance = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 1g.5gb
          placement_start: 0
`

const migExplicitOneLargeInstance = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 3g.20gb
          placement_start: 0
`

// migExplicitMoved moves instance 0 of migExplicitTwo to a free offset and
// leaves its neighbour where it is. Hardware cannot move an instance in place,
// so this is a replacement of instance 0 rather than an edit of it.
const migExplicitMoved = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 1g.5gb
          placement_start: 3
        - id: 1
          profile: 1g.5gb
          placement_start: 1
`

// The three documents below are the same subdivided GPU instance recorded with
// two compute instances, with one, and with nothing said about them at all —
// the states `nvidia-smi mig -cci` and `mig -dci` write down.
const migExplicitTwoComputeInstances = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 3g.20gb
          placement_start: 0
          compute_instances:
            - id: 0
              profile: 1c
            - id: 1
              profile: 1c
`

const migExplicitOneComputeInstance = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 3g.20gb
          placement_start: 0
          compute_instances:
            - id: 0
              profile: 1c
`

const migExplicitNoComputeInstanceList = `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances:
        - id: 0
          profile: 3g.20gb
          placement_start: 0
`

// a100PlacementCapacity is how many 1-slice partitions an A100's placement
// grid holds — the real ceiling on what these boards can enumerate, unlike
// GetMaxMigDeviceCount, which an override can move at runtime.
const a100PlacementCapacity = 7

// migPartitionCount counts the MIG devices NVML enumerates on a board. It walks
// the whole index range instead of stopping at the first miss so a sparse
// enumeration cannot read as an empty one, and it walks the placement grid
// rather than the reported ceiling so a document that lowers the ceiling below
// the live partition count still counts every partition.
func migPartitionCount(t *testing.T, dev *ConfigurableDevice) int {
	t.Helper()
	maxCount, ret := dev.GetMaxMigDeviceCount()
	require.Equal(t, nvml.SUCCESS, ret)
	count := 0
	for i := range max(maxCount, a100PlacementCapacity) {
		if _, ret := dev.GetMigDeviceHandleByIndex(i); ret == nvml.SUCCESS {
			count++
		}
	}
	return count
}

// TestReconcileMIG_OverrideUpdatesMaxGPUInstances: a runtime override that
// only lowers the ceiling must be visible through GetMaxMigDeviceCount without
// conflating the ceiling with how many partitions the override materializes.
func TestReconcileMIG_OverrideUpdatesMaxGPUInstances(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())

	maxBefore, ret := dev.GetMaxMigDeviceCount()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 7, maxBefore)

	writeConfigOverride(t, path, migEnableMax3, clock)

	current, pending, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.DEVICE_MIG_ENABLE, current)
	require.Equal(t, nvml.DEVICE_MIG_ENABLE, pending)

	maxAfter, ret := dev.GetMaxMigDeviceCount()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 3, maxAfter)
}

// TestReconcileMIG_OverrideEnablesPartitioning is the feature: a board that
// booted with MIG off partitions itself when an override turns it on, with no
// restart.
func TestReconcileMIG_OverrideEnablesPartitioning(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	require.Equal(t, 0, migPartitionCount(t, dev), "board should boot unpartitioned")

	writeConfigOverride(t, path, migEnable7x1g, clock)

	current, pending, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.DEVICE_MIG_ENABLE, current)
	require.Equal(t, nvml.DEVICE_MIG_ENABLE, pending)
	require.Equal(t, 7, migPartitionCount(t, dev))
}

// TestReconcileMIG_OverrideAlreadyOnDiskAtConstruction is the ordering
// production has and every other test in this file does not: the CLI writes
// the document and the consumer process starts afterwards, so the device is
// built with the override already on disk. Its partitioning must be the
// document's, not the profile's.
func TestReconcileMIG_OverrideAlreadyOnDiskAtConstruction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	// Written before the device exists, which is the whole point of the test;
	// newTestDevice cannot be used because it constructs the device first.
	require.NoError(t, os.WriteFile(path, []byte(migEnable2x3g), 0o644))

	now := time.Unix(0, 0)
	configOverrides = newConfigOverrideStoreAt(
		func() string { return path },
		func() time.Time { return now },
	)
	t.Cleanup(resetConfigOverrideStoreForTesting)

	srv := dgxa100.New()
	baseDevice, ok := srv.Devices[0].(*mockserver.Device)
	require.True(t, ok)
	// A profile that boots partitioned seven ways, so the document's two
	// partitions cannot be confused with the profile's own layout.
	dev := NewConfigurableDevice(0, baseDevice, a100PartitionedConfig(), "GPU-test", "0000:01:00.0", 0, nil)

	require.Equal(t, 2, migPartitionCount(t, dev),
		"a device built while an override is on disk should boot with the override's layout")
	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	name, ret := migDev.GetName()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Contains(t, name, "3g.20gb", "the override's profile should be the one reported")
}

// TestReconcileMIG_OverrideSwapsLayout is the case a merged write would get
// wrong: the new layout must replace the old one, not add to it.
func TestReconcileMIG_OverrideSwapsLayout(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migEnable7x1g, clock)
	require.Equal(t, 7, migPartitionCount(t, dev))

	writeConfigOverride(t, path, migEnable2x3g, clock)

	require.Equal(t, 2, migPartitionCount(t, dev))
	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	name, ret := migDev.GetName()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Contains(t, name, "3g.20gb", "the swapped-in profile should be the one reported")
}

func TestReconcileMIG_OverrideDisablesPartitioning(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migEnable7x1g, clock)
	require.Equal(t, 7, migPartitionCount(t, dev))

	writeConfigOverride(t, path, migDisable, clock)

	current, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.DEVICE_MIG_DISABLE, current)
	require.Equal(t, 0, migPartitionCount(t, dev))
}

// TestReconcileMIG_UnrelatedOverrideLeavesPartitioningAlone guards the property
// that makes the reconciler safe to run on every refresh: pinning a temperature
// must not tear a partitioned board down and rebuild it, which would invalidate
// handles a consumer is holding for no reason.
//
// The board is partitioned by its profile here, not by an override, because
// that is the case where a document naming only a temperature still leaves the
// merged MIG config identical to the applied one.
func TestReconcileMIG_UnrelatedOverrideLeavesPartitioningAlone(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100PartitionedConfig())
	require.Equal(t, 7, migPartitionCount(t, dev))
	before, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, "devices:\n  \"0\":\n    thermal:\n      temperature_gpu_c: 85\n", clock)

	require.Equal(t, 7, migPartitionCount(t, dev))
	after, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, before, after, "an unrelated override must not rebuild the partitioning")
}

// TestReconcileMIG_RemovingTheOverrideRestoresTheProfile: overrides are a whole
// document, so dropping the mig block returns the board to the state its
// profile declares rather than freezing the last override.
func TestReconcileMIG_RemovingTheOverrideRestoresTheProfile(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100PartitionedConfig())
	writeConfigOverride(t, path, migEnable2x3g, clock)
	require.Equal(t, 2, migPartitionCount(t, dev))

	require.NoError(t, os.Remove(path))
	*clock = clock.Add(2 * time.Second)

	require.Equal(t, 7, migPartitionCount(t, dev),
		"the profile's declared layout should come back")
}

// TestReconcileMIG_IsIdempotent: rewriting the same block must not rebuild, so
// a consumer's MIG device stays the same object across an unrelated document
// bump.
func TestReconcileMIG_IsIdempotent(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migEnable7x1g, clock)
	before, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, migEnable7x1g+"    thermal:\n      temperature_gpu_c: 85\n", clock)

	after, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, before, after, "an unchanged mig block must not rebuild the partitioning")
}

// TestReconcileMIG_RetiresDestroyedMigDevices covers the hook contract: the
// engine has to learn which MIG devices vanished, or a consumer keeps a handle
// to a destroyed instance.
func TestReconcileMIG_RetiresDestroyedMigDevices(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migEnable7x1g, clock)

	var retired []*ConfigurableDevice
	dev.onRepartition = func(devices []*ConfigurableDevice) { retired = devices }
	doomed, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, migEnable2x3g, clock)
	// GetMigMode drives refresh, which is what retires the old MIG devices.
	_, _, ret = dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)

	require.Len(t, retired, 7, "every MIG device of the old layout should be reported")
	require.Contains(t, retired, doomed)
}

// TestReconcileMIG_ExplicitAddLeavesNeighborsAlone: another process adding an
// instance must not disturb the ones this process is already holding handles
// to.
func TestReconcileMIG_ExplicitAddLeavesNeighborsAlone(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitTwo, clock)
	require.Equal(t, 2, migPartitionCount(t, dev))

	before, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, migExplicitThree, clock)

	require.Equal(t, 3, migPartitionCount(t, dev))
	after, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, before, after, "adding an instance must not rebuild the survivors")
}

// TestReconcileMIG_ExplicitDeletePreservesSurvivorIDs: a delete removes exactly
// the instance named and leaves the IDs of the survivors alone. The MIG devices
// of the instance it does remove have to be retired, or a consumer keeps a
// handle that answers for a partition that is gone.
func TestReconcileMIG_ExplicitDeletePreservesSurvivorIDs(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitThree, clock)
	require.Equal(t, 3, migPartitionCount(t, dev))

	var retired []*ConfigurableDevice
	dev.onRepartition = func(devices []*ConfigurableDevice) { retired = devices }
	survivor, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	// MIG devices enumerate in GPU instance order, so index 1 is the partition
	// of instance 1, the one migExplicitGap drops.
	doomed, ret := dev.GetMigDeviceHandleByIndex(1)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, migExplicitGap, clock)

	// GetMigMode drives the refresh that reaches the reconciler.
	_, _, ret = dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{0, 2}, liveGpuInstanceIDs(t, dev))

	// The IDs alone would survive a rebuild, since the layout pins them; only
	// the handle proves the survivor was never torn down.
	after, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, survivor, after, "deleting one instance must not rebuild the others")

	require.Contains(t, retired, doomed,
		"the MIG device of the deleted instance must be reported for retirement")
	require.NotContains(t, retired, survivor, "a survivor's MIG device must stay valid")
}

// TestReconcileMIG_ExplicitEmptyListClearsTheBoard: an empty explicit list
// leaves a MIG-enabled board bare instead of falling back to the profile's
// declared partitions.
func TestReconcileMIG_ExplicitEmptyListClearsTheBoard(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100PartitionedConfig())
	require.Equal(t, 7, migPartitionCount(t, dev))

	writeConfigOverride(t, path, `devices:
  "0":
    mig:
      mode_current: enabled
      mode_pending: enabled
      instances: []
`, clock)

	_, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 0, migPartitionCount(t, dev))
}

// TestReconcileMIG_ExplicitRewriteIsANoOp: rewriting the same explicit layout
// must not rebuild, which is the property that makes a process's own write-back
// invisible to itself.
func TestReconcileMIG_ExplicitRewriteIsANoOp(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitTwo, clock)
	before, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, migExplicitTwo+"    thermal:\n      temperature_gpu_c: 85\n", clock)

	after, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, before, after, "an unchanged layout must not rebuild")
}

// liveGpuInstance returns the live GPU instance with the given ID. It reads
// the board directly rather than through NVML, so callers must have driven a
// refresh themselves.
func liveGpuInstance(t *testing.T, dev *ConfigurableDevice, id uint32) *mockserver.GpuInstance {
	t.Helper()
	st := dev.migState
	st.mu.Lock()
	defer st.mu.Unlock()

	for _, gi := range st.liveGpuInstances(dev) {
		if gi.Info.Id == id {
			return gi
		}
	}
	require.FailNowf(t, "no such GPU instance", "device has no live GPU instance %d", id)
	return nil
}

func computeInstanceIDs(cis []*mockserver.ComputeInstance) []uint32 {
	ids := make([]uint32, 0, len(cis))
	for _, ci := range cis {
		ids = append(ids, ci.Info.Id)
	}
	return ids
}

// TestReconcileMIG_ExplicitAddsARecordedComputeInstance: a compute instance
// created in one process has to cross into another like any other mutation,
// and without disturbing the GPU instance it subdivides — nobody asked for the
// siblings, or the handles held to them, to be rebuilt.
func TestReconcileMIG_ExplicitAddsARecordedComputeInstance(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitOneComputeInstance, clock)
	require.Equal(t, 1, migPartitionCount(t, dev))

	giBefore := liveGpuInstance(t, dev, 0)
	cisBefore := liveComputeInstances(giBefore)
	require.Equal(t, []uint32{0}, computeInstanceIDs(cisBefore))
	migBefore, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, migExplicitTwoComputeInstances, clock)
	// GetMigMode drives the refresh that reaches the reconciler.
	_, _, ret = dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)

	giAfter := liveGpuInstance(t, dev, 0)
	require.Same(t, giBefore, giAfter, "adding a compute instance must not rebuild its GPU instance")
	cisAfter := liveComputeInstances(giAfter)
	require.Equal(t, []uint32{0, 1}, computeInstanceIDs(cisAfter),
		"the recorded compute instance should have been created")
	require.Same(t, cisBefore[0], cisAfter[0], "an untouched compute instance must not be rebuilt")

	migAfter, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, migBefore, migAfter, "the MIG device of an untouched compute instance must stay valid")
	require.Equal(t, 2, migPartitionCount(t, dev))
}

// TestReconcileMIG_ExplicitRemovesADroppedComputeInstance is the counterpart:
// a compute instance the layout no longer records goes away, and its siblings
// and the GPU instance around it do not. Its MIG device has to be retired,
// since that partition no longer exists to answer for.
func TestReconcileMIG_ExplicitRemovesADroppedComputeInstance(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitTwoComputeInstances, clock)
	require.Equal(t, 2, migPartitionCount(t, dev))

	var retired []*ConfigurableDevice
	dev.onRepartition = func(devices []*ConfigurableDevice) { retired = devices }

	giBefore := liveGpuInstance(t, dev, 0)
	cisBefore := liveComputeInstances(giBefore)
	require.Equal(t, []uint32{0, 1}, computeInstanceIDs(cisBefore))
	migBefore, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	// MIG devices enumerate in compute instance order within a GPU instance,
	// so index 1 is the partition of the compute instance being dropped.
	doomed, ret := dev.GetMigDeviceHandleByIndex(1)
	require.Equal(t, nvml.SUCCESS, ret)

	writeConfigOverride(t, path, migExplicitOneComputeInstance, clock)
	_, _, ret = dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)

	giAfter := liveGpuInstance(t, dev, 0)
	require.Same(t, giBefore, giAfter, "deleting a compute instance must not rebuild its GPU instance")
	cisAfter := liveComputeInstances(giAfter)
	require.Equal(t, []uint32{0}, computeInstanceIDs(cisAfter),
		"the compute instance the layout dropped should be gone")
	require.Same(t, cisBefore[0], cisAfter[0], "the surviving compute instance must not be rebuilt")

	migAfter, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Same(t, migBefore, migAfter, "the MIG device of the survivor must stay valid")
	require.Equal(t, 1, migPartitionCount(t, dev))

	require.Contains(t, retired, doomed,
		"the MIG device of the deleted compute instance must be reported for retirement")
	require.NotContains(t, retired, migBefore, "the survivor's MIG device must not be retired")
}

// TestReconcileMIG_ExplicitPlacementChangeReplacesTheInstance: an instance
// cannot be moved in place on hardware, so a record whose placement no longer
// matches describes a new instance under a reused ID and has to be
// materialized as one.
func TestReconcileMIG_ExplicitPlacementChangeReplacesTheInstance(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitTwo, clock)
	require.Equal(t, 2, migPartitionCount(t, dev))

	before := liveGpuInstance(t, dev, 0)
	neighbour := liveGpuInstance(t, dev, 1)

	writeConfigOverride(t, path, migExplicitMoved, clock)
	_, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)

	moved := liveGpuInstance(t, dev, 0)
	require.NotSame(t, before, moved,
		"a moved instance must be replaced, not edited in place")
	require.Equal(t, uint32(3), moved.Info.Placement.Start,
		"the instance should have been recreated at the recorded offset")
	require.Same(t, neighbour, liveGpuInstance(t, dev, 1),
		"replacing one instance must not rebuild the others")
	require.Equal(t, 2, migPartitionCount(t, dev))
}

// TestReconcileMIG_ExplicitProfileChangeReplacesTheInstance is the placement
// case's twin on the other thing hardware cannot change in place: a record
// reusing an ID under a wider profile describes a new instance, so the live one
// has to be torn down and the record materialized in its stead.
func TestReconcileMIG_ExplicitProfileChangeReplacesTheInstance(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitOneSmallInstance, clock)
	require.Equal(t, 1, migPartitionCount(t, dev))

	before := liveGpuInstance(t, dev, 0)

	writeConfigOverride(t, path, migExplicitOneLargeInstance, clock)
	_, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)

	replaced := liveGpuInstance(t, dev, 0)
	require.NotSame(t, before, replaced,
		"a reprofiled instance must be replaced, not edited in place")
	require.Equal(t, []uint32{0}, liveGpuInstanceIDs(t, dev))

	// The profile is read off the partition rather than the instance, so the
	// assertion covers the compute instance the replacement was given too.
	require.Equal(t, 1, migPartitionCount(t, dev))
	migDev, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	name, ret := migDev.GetName()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Contains(t, name, "3g.20gb", "the recorded profile should be the live one")
}

// TestReconcileMIG_ExplicitUnspecifiedComputeInstancesAreLeftAlone pins what a
// record saying nothing about compute instances means to the diff: unspecified,
// not empty. The same record materialized the spanning default when it created
// the instance, so reading nil as "none" would delete that default; and a
// record hand-written without the key would delete compute instances a
// consumer created through NVML and still holds.
func TestReconcileMIG_ExplicitUnspecifiedComputeInstancesAreLeftAlone(t *testing.T) {
	dev, path, clock := newTestDevice(t, a100MIGConfig())
	writeConfigOverride(t, path, migExplicitTwoComputeInstances, clock)
	require.Equal(t, 2, migPartitionCount(t, dev))

	cisBefore := liveComputeInstances(liveGpuInstance(t, dev, 0))
	require.Equal(t, []uint32{0, 1}, computeInstanceIDs(cisBefore))

	writeConfigOverride(t, path, migExplicitNoComputeInstanceList, clock)
	_, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.SUCCESS, ret)

	cisAfter := liveComputeInstances(liveGpuInstance(t, dev, 0))
	require.Equal(t, []uint32{0, 1}, computeInstanceIDs(cisAfter),
		"an unspecified compute instance list must not clear the live ones")
	require.Same(t, cisBefore[0], cisAfter[0])
	require.Same(t, cisBefore[1], cisAfter[1])
}

// TestReconcileMIG_IgnoresBoardsWithoutMIG: faking MIG on a T4 would report a
// board that cannot exist.
func TestReconcileMIG_IgnoresBoardsWithoutMIG(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{Name: "Tesla T4"})

	writeConfigOverride(t, path, migEnable7x1g, clock)

	_, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// TestReconcileMIG_WarnsOnPendingOnlyOverrideWithoutMIG: a document that only
// stages MIG for the next reset is still an override its author expects to take
// effect, so a board that cannot honour it has to say so. Dropping the request
// in silence is what leaves an operator reading an unpartitioned board with no
// account of why.
func TestReconcileMIG_WarnsOnPendingOnlyOverrideWithoutMIG(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{Name: "Tesla T4"})

	var ret nvml.Return
	stderr := captureStderr(t, func() {
		writeConfigOverride(t, path, migEnablePendingOnly, clock)
		// GetMigMode drives the refresh that reaches the reconciler.
		_, _, ret = dev.GetMigMode()
	})

	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	require.Contains(t, stderr, "not MIG-capable",
		"a pending-only enable on a board without MIG should be diagnosed")
}

// captureStderr returns what fn wrote to the process's stderr, which is where
// warnLog puts diagnostics and therefore the only place they can be asserted
// from. The redirection is process-wide, so callers must not run in parallel.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr")
	file, err := os.Create(path)
	require.NoError(t, err)

	saved := os.Stderr
	os.Stderr = file
	// Deferred so a failed assertion inside fn, which unwinds the goroutine,
	// cannot leave the rest of the run without its stderr.
	defer func() {
		os.Stderr = saved
		require.NoError(t, file.Close())
	}()

	fn()

	// warnLog writes unbuffered, so everything fn emitted is already on disk.
	out, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(out)
}

// TestReconcileMIG_ConcurrentCeilingReads: the MIG ceiling stopped being
// immutable when a reconciler started writing it, so a consumer polling it
// from another goroutine now races the rebuild. The assertion on the concurrent
// reads is deliberately weak — only that every ceiling observed is one the
// documents in play can produce — because what those reads exist to surface is
// the -race report, not a wrong value. Convergence is asserted serially once
// the reader is gone, where a value can actually be wrong.
func TestReconcileMIG_ConcurrentCeilingReads(t *testing.T) {
	dev, path, _ := newTestDevice(t, a100MIGConfig())

	// newTestDevice's clock is a plain *time.Time that both goroutines would
	// touch; swap in an atomic one so the access under test is the only
	// unsynchronized one left.
	var nanos atomic.Int64
	configOverrides = newConfigOverrideStoreAt(
		func() string { return path },
		func() time.Time { return time.Unix(0, nanos.Load()) },
	)

	type observation struct {
		count int
		ret   nvml.Return
	}
	var (
		polls   int
		illegal []observation
		reader  sync.WaitGroup
	)
	done := make(chan struct{})
	reader.Add(1)
	go func() {
		defer reader.Done()
		// Capped so this stays a short overlap rather than a stress loop; the
		// writes below are slow enough that a few thousand reads span them.
		const maxPolls = 20000
		for polls < maxPolls {
			select {
			case <-done:
				return
			default:
			}
			polls++
			count, ret := dev.GetMaxMigDeviceCount()
			if ret != nvml.SUCCESS || (count != 3 && count != a100PlacementCapacity) {
				illegal = append(illegal, observation{count: count, ret: ret})
			}
		}
	}()

	// Alternating the two documents moves the ceiling between 7 and 3 on every
	// generation, so each one is a write the reader can catch in flight.
	for i := range 8 {
		doc := migEnableMax3
		if i%2 == 0 {
			doc = migEnable7x1g
		}
		require.NoError(t, os.WriteFile(path, []byte(doc), 0o644))
		nanos.Add(int64(2 * time.Second))
		// GetMigMode drives the refresh that repartitions and moves the ceiling.
		_, _, ret := dev.GetMigMode()
		require.Equal(t, nvml.SUCCESS, ret)
	}
	close(done)
	reader.Wait()

	require.Positive(t, polls, "the reader must have observed the ceiling at least once")
	require.Empty(t, illegal, "every ceiling read must be one the overrides declare")

	// Surviving the race is not the same as landing on the right value: a
	// refresh that lost the TryLock to the reader has to be picked up by a
	// later one, so the last document written is what the board must settle on.
	settled, ret := dev.GetMaxMigDeviceCount()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 3, settled, "the ceiling should converge on the last document's")
}

// a100NodeConfig is a one-GPU A100 node whose board UUID is pinned. A
// partition's UUID is derived from its board's, and the mock generates a random
// board UUID per server, so pinning it is what lets a layout compiled from one
// config be compared against a board built from another. Passing nil leaves the
// board with MIG off, the state a100MIGConfig declares.
func a100NodeConfig(mig *MIGConfig) *Config {
	dev := a100MIGConfig()
	if mig != nil {
		dev.MIG = mig
	}
	return &Config{
		NumDevices: 1,
		YAMLConfig: &YAMLConfig{
			Version:        "1.0",
			DeviceDefaults: *dev,
			Devices:        []DeviceOverride{{Index: 0, UUID: "GPU-11111111-2222-3333-4444-555555555555"}},
		},
	}
}

// TestReconcileMIG_UUIDLookupFollowsARepartition: DeviceGetHandleByUUID is the
// one MIG read no handle guard precedes, and the device plugin's health monitor
// places a partition through it, so a UUID belonging to the layout a document
// just asked for has to resolve without any other read having refreshed first.
func TestReconcileMIG_UUIDLookupFollowsARepartition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	now := time.Unix(0, 0)
	configOverrides = newConfigOverrideStoreAt(
		func() string { return path },
		func() time.Time { return now },
	)
	t.Cleanup(resetConfigOverrideStoreForTesting)

	// The UUID nvml-mock-ctl predicts for the layout it is about to request,
	// compiled from a config rather than read off the live board: resolving it
	// through the engine first would refresh the board, which is the very thing
	// under test.
	predicted := DeclaredMIGLayout(a100NodeConfig(&MIGConfig{
		ModeCurrent:  "enabled",
		ModePending:  "enabled",
		GPUInstances: []MIGGPUInstanceConfig{{Profile: "3g.20gb", Count: 2}},
	}))
	require.Len(t, predicted, 1)
	require.Len(t, predicted[0].GPUInstances, 2)
	require.NotEmpty(t, predicted[0].GPUInstances[1].ComputeInstances)
	uuid := predicted[0].GPUInstances[1].ComputeInstances[0].UUID
	require.NotEmpty(t, uuid)

	e := NewEngine(a100NodeConfig(nil))
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })

	require.NoError(t, os.WriteFile(path, []byte(migEnable2x3g), 0o644))
	now = now.Add(2 * time.Second)

	handle, ret := e.DeviceGetHandleByUUID(uuid)
	require.Equal(t, nvml.SUCCESS, ret,
		"a partition of the layout the document asked for must resolve by UUID")
	isMig, ret := e.LookupConfigurableDevice(handle).IsMigDeviceHandle()
	require.Equal(t, nvml.SUCCESS, ret)
	require.True(t, isMig, "%s resolved to a handle that denies being a MIG device", uuid)
}

// TestDeclaredMIGLayout_IgnoresOverridesOnDisk: the declared layout is what
// the config it is given partitions into, not what the node happens to be
// partitioned into. nvml-mock-ctl validates a requested layout against it
// before writing the document that would produce it, so a layout that folded
// in the live document would answer for the previous request instead of the
// new one.
func TestDeclaredMIGLayout_IgnoresOverridesOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")
	require.NoError(t, os.WriteFile(path, []byte(migEnable2x3g), 0o644))

	now := time.Unix(0, 0)
	configOverrides = newConfigOverrideStoreAt(
		func() string { return path },
		func() time.Time { return now },
	)
	t.Cleanup(resetConfigOverrideStoreForTesting)

	layout := DeclaredMIGLayout(&Config{
		NumDevices: 1,
		YAMLConfig: &YAMLConfig{Version: "1.0", DeviceDefaults: *a100PartitionedConfig()},
	})

	require.Len(t, layout, 1)
	require.Len(t, layout[0].GPUInstances, a100PlacementCapacity,
		"the config's own layout, not the document's two partitions")
}

// TestCreateServer_WiresRepartitionHook: the reconciler's handle retirement is
// only real if the engine actually connects it.
func TestCreateServer_WiresRepartitionHook(t *testing.T) {
	t.Parallel()

	server, err := NewEngine(&Config{
		NumDevices:    1,
		DriverVersion: "550.163",
		YAMLConfig: &YAMLConfig{
			Version:        "1.0",
			System:         SystemConfig{DriverVersion: "550.163", NVMLVersion: "12.550.163", NumDevices: 1},
			DeviceDefaults: *a100MIGConfig(),
		},
	}).createServer()
	require.NoError(t, err)
	require.NotNil(t, server.configurableDevices[0].onRepartition,
		"a repartition must be able to retire the handles the engine owns")
}
