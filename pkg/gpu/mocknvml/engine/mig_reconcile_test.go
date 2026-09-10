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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// These tests drive the package-global config override store through
// newTestDevice, so none of them may run in parallel.

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

// a100PlacementCapacity is how many 1-slice partitions an A100's placement
// grid holds — the real ceiling on what these boards can enumerate, unlike
// GetMaxMigDeviceCount, which an override can move at runtime.
const a100PlacementCapacity = 7

// migPartitionCount counts the MIG devices NVML enumerates on a board. It walks
// the whole index range instead of stopping at the first miss so a sparse
// enumeration cannot read as an empty one, and it walks past a lowered ceiling
// so a document that raises both the ceiling and the layout still counts every
// partition.
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

// TestReconcileMIG_IgnoresBoardsWithoutMIG: faking MIG on a T4 would report a
// board that cannot exist.
func TestReconcileMIG_IgnoresBoardsWithoutMIG(t *testing.T) {
	dev, path, clock := newTestDevice(t, &DeviceConfig{Name: "Tesla T4"})

	writeConfigOverride(t, path, migEnable7x1g, clock)

	_, _, ret := dev.GetMigMode()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// TestReconcileMIG_ConcurrentCeilingReads: the MIG ceiling stopped being
// immutable when a reconciler started writing it, so a consumer polling it
// from another goroutine now races the rebuild. The assertion is deliberately
// weak — only that every ceiling observed is one the documents in play can
// produce — because what this test exists to surface is the -race report, not
// a wrong value.
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
