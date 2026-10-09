// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

var drainNodeBDFs = []string{"0000:0A:00.0", "0000:0B:00.0", "0000:0C:00.0"}

// drainNodeConfig is a three-GPU node at drainNodeBDFs. tweak adjusts the
// per-device entries, for a profile that marks one GPU differently.
func drainNodeConfig(base DeviceConfig, tweak func(devs []DeviceOverride)) *Config {
	devs := make([]DeviceOverride, len(drainNodeBDFs))
	for i, bdf := range drainNodeBDFs {
		devs[i] = devWithBDF(i, bdf)
		devs[i].UUID = fmt.Sprintf("GPU-4d4f434b-0000-0000-0000-%012d", i)
	}
	if tweak != nil {
		tweak(devs)
	}
	return &Config{
		NumDevices:    len(devs),
		DriverVersion: "550.163",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "550.163",
				NVMLVersion:   "12.550.163",
				NumDevices:    len(devs),
			},
			DeviceDefaults: base,
			Devices:        devs,
		},
	}
}

// startProcess initialises an engine the way a newly started consumer would.
// Engines started in one test share the override document, which is the only
// state separate processes share.
func startProcess(t *testing.T, cfg *Config) *Engine {
	t.Helper()
	e := NewEngine(cfg)
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })
	return e
}

func pciAt(t *testing.T, bdf string) *nvml.PciInfo {
	t.Helper()
	domain, bus, device, _, err := ParsePCIBusID(bdf)
	require.NoError(t, err)
	return &nvml.PciInfo{Domain: domain, Bus: bus, Device: device}
}

// enumerated lists the PCI addresses an engine enumerates, in index order.
func enumerated(t *testing.T, e *Engine) []string {
	t.Helper()
	n, ret := e.DeviceGetCount()
	require.Equal(t, nvml.SUCCESS, ret)
	out := make([]string, 0, n)
	for i := range n {
		h, ret := e.DeviceGetHandleByIndex(i)
		require.Equal(t, nvml.SUCCESS, ret)
		out = append(out, e.LookupConfigurableDevice(h).PciBusID)
	}
	return out
}

func requireDrainState(t *testing.T, e *Engine, bdf string, want nvml.EnableState) {
	t.Helper()
	got, ret := e.QueryDrainState(pciAt(t, bdf))
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, want, got)
}

func TestQueryDrainState_DefaultsToNotDraining(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))

	requireDrainState(t, e, "0000:0B:00.0", nvml.FEATURE_DISABLED)
}

func TestModifyDrainState_RoundTrips(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))
	pci := pciAt(t, "0000:0B:00.0")

	require.Equal(t, nvml.SUCCESS, e.ModifyDrainState(pci, nvml.FEATURE_ENABLED))
	requireDrainState(t, e, "0000:0B:00.0", nvml.FEATURE_ENABLED)
	requireDrainState(t, e, "0000:0A:00.0", nvml.FEATURE_DISABLED)

	require.Equal(t, nvml.SUCCESS, e.ModifyDrainState(pci, nvml.FEATURE_DISABLED))
	requireDrainState(t, e, "0000:0B:00.0", nvml.FEATURE_DISABLED)
}

// TestModifyDrainState_VisibleToAnotherProcess covers `nvidia-smi drain -m 1`
// followed by a separate `nvidia-smi drain -q`: drain state is driver state.
func TestModifyDrainState_VisibleToAnotherProcess(t *testing.T) {
	persistSetterWrites(t)
	cfg := drainNodeConfig(DeviceConfig{}, nil)
	writer := startProcess(t, cfg)

	require.Equal(t, nvml.SUCCESS, writer.ModifyDrainState(pciAt(t, "0000:0B:00.0"), nvml.FEATURE_ENABLED))

	requireDrainState(t, startProcess(t, cfg), "0000:0B:00.0", nvml.FEATURE_ENABLED)
}

// TestModifyDrainState_RefusedWhilePersistent pins the documented
// precondition: persistence mode keeps the GPU attached, so it must be turned
// off before the GPU can drain.
func TestModifyDrainState_RefusedWhilePersistent(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{PersistenceMode: "enabled"}, nil))
	pci := pciAt(t, "0000:0B:00.0")

	require.Equal(t, nvml.ERROR_IN_USE, e.ModifyDrainState(pci, nvml.FEATURE_ENABLED))
	requireDrainState(t, e, "0000:0B:00.0", nvml.FEATURE_DISABLED)

	h, ret := e.DeviceGetHandleByPciBusId("0000:0B:00.0")
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.SUCCESS, e.LookupConfigurableDevice(h).SetPersistenceMode(nvml.FEATURE_DISABLED))
	require.Equal(t, nvml.SUCCESS, e.ModifyDrainState(pci, nvml.FEATURE_ENABLED))
}

func TestModifyDrainState_RejectsUnknownState(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.ModifyDrainState(pciAt(t, "0000:0B:00.0"), nvml.EnableState(2)))
}

func TestDrainState_RejectsUnknownAddress(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))

	for _, pci := range []*nvml.PciInfo{nil, pciAt(t, "0000:0F:00.0")} {
		_, ret := e.QueryDrainState(pci)
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.ModifyDrainState(pci, nvml.FEATURE_ENABLED))
	}
}

// TestDrainState_ResolvesBusIDString covers a caller that fills only the busId
// string of nvmlPciInfo_t and leaves the numeric fields zeroed.
func TestDrainState_ResolvesBusIDString(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))
	var pci nvml.PciInfo
	writeBusID(pci.BusId[:], "00000000:0B:00.0")

	require.Equal(t, nvml.SUCCESS, e.ModifyDrainState(&pci, nvml.FEATURE_ENABLED))
	requireDrainState(t, e, "0000:0B:00.0", nvml.FEATURE_ENABLED)
}

// TestDrainingGPU_DropsOutOfTheNextProcess pins what draining is for: a
// process that initialises NVML afterwards no longer sees the GPU, and the
// GPUs after it move down one index. The process that drained it keeps its
// view, and the GPU stays addressable by PCI location so it can be undrained.
func TestDrainingGPU_DropsOutOfTheNextProcess(t *testing.T) {
	persistSetterWrites(t)
	cfg := drainNodeConfig(DeviceConfig{}, nil)
	writer := startProcess(t, cfg)
	pci := pciAt(t, "0000:0B:00.0")

	require.Equal(t, nvml.SUCCESS, writer.ModifyDrainState(pci, nvml.FEATURE_ENABLED))
	require.Equal(t, drainNodeBDFs, enumerated(t, writer))

	next := startProcess(t, cfg)
	require.Equal(t, []string{"0000:0A:00.0", "0000:0C:00.0"}, enumerated(t, next))
	_, ret := next.DeviceGetHandleByPciBusId("0000:0B:00.0")
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)
	_, ret = next.DeviceGetHandleByUUID("GPU-4d4f434b-0000-0000-0000-000000000001")
	require.Equal(t, nvml.ERROR_NOT_FOUND, ret)

	requireDrainState(t, next, "0000:0B:00.0", nvml.FEATURE_ENABLED)
	require.Equal(t, nvml.SUCCESS, next.ModifyDrainState(pci, nvml.FEATURE_DISABLED))
	require.Equal(t, drainNodeBDFs, enumerated(t, startProcess(t, cfg)))
}

// TestEnumerable_ComposesWithDeviceNodeFiltering covers a container that was
// handed only some /dev/nvidia* nodes: a draining GPU narrows that set further
// rather than replacing it.
func TestEnumerable_ComposesWithDeviceNodeFiltering(t *testing.T) {
	persistSetterWrites(t)
	cfg := drainNodeConfig(DeviceConfig{}, nil)
	e := startProcess(t, cfg)
	require.Nil(t, e.server.enumerable(nil, false), "nothing hidden leaves enumeration unfiltered")

	require.Equal(t, nvml.SUCCESS, e.ModifyDrainState(pciAt(t, "0000:0A:00.0"), nvml.FEATURE_ENABLED))

	require.Equal(t, []int{2}, e.server.enumerable([]int{0, 2}, false))
	require.Equal(t, []int{1, 2}, e.server.enumerable(nil, false))
	require.Equal(t, []int{}, e.server.enumerable([]int{0}, false),
		"an empty set hides every GPU, unlike nil which hides none")
}

func removeGpu(e *Engine, pci *nvml.PciInfo) nvml.Return {
	return e.RemoveGpu(pci, nvml.DETACH_GPU_REMOVE, nvml.PCIE_LINK_KEEP)
}

// TestRemoveGpu_RenumbersEveryProcess covers `nvidia-smi drain -r`. Unlike a
// drain, removal changes the enumeration of the calling process as well.
func TestRemoveGpu_RenumbersEveryProcess(t *testing.T) {
	persistSetterWrites(t)
	cfg := drainNodeConfig(DeviceConfig{}, nil)
	e := startProcess(t, cfg)
	pci := pciAt(t, "0000:0B:00.0")

	require.Equal(t, nvml.SUCCESS, removeGpu(e, pci))

	remaining := []string{"0000:0A:00.0", "0000:0C:00.0"}
	require.Equal(t, remaining, enumerated(t, e))
	require.Equal(t, remaining, enumerated(t, startProcess(t, cfg)))

	_, ret := e.QueryDrainState(pci)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "a removed GPU is gone from the driver")
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, removeGpu(e, pci))
}

// TestRemoveGpu_KeepsGpusThisProcessSawDrain: removal and rediscovery
// renumber around the GPU they act on. A GPU this process enumerated before
// it started draining stays in its view, as it would after a drain alone.
func TestRemoveGpu_KeepsGpusThisProcessSawDrain(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))
	require.Equal(t, nvml.SUCCESS, e.ModifyDrainState(pciAt(t, "0000:0B:00.0"), nvml.FEATURE_ENABLED))

	require.Equal(t, nvml.SUCCESS, removeGpu(e, pciAt(t, "0000:0A:00.0")))
	require.Equal(t, []string{"0000:0B:00.0", "0000:0C:00.0"}, enumerated(t, e))

	require.Equal(t, nvml.SUCCESS, e.DiscoverGpus(&nvml.PciInfo{}))
	require.Equal(t, drainNodeBDFs, enumerated(t, e))
}

// TestRemoveGpu_KeepsHidingGpusDrainedBeforeInit is the converse: a GPU that
// was already draining when this process initialised does not reappear when
// the process renumbers around another one.
func TestRemoveGpu_KeepsHidingGpusDrainedBeforeInit(t *testing.T) {
	persistSetterWrites(t)
	cfg := drainNodeConfig(DeviceConfig{}, nil)
	require.Equal(t, nvml.SUCCESS,
		startProcess(t, cfg).ModifyDrainState(pciAt(t, "0000:0B:00.0"), nvml.FEATURE_ENABLED))
	e := startProcess(t, cfg)

	require.Equal(t, nvml.SUCCESS, removeGpu(e, pciAt(t, "0000:0A:00.0")))
	require.Equal(t, []string{"0000:0C:00.0"}, enumerated(t, e))

	require.Equal(t, nvml.SUCCESS, e.DiscoverGpus(&nvml.PciInfo{}))
	require.Equal(t, []string{"0000:0A:00.0", "0000:0C:00.0"}, enumerated(t, e))
}

// TestRemoveGpu_RefusedWhileAttached covers the two attachments the mock can
// see. Persistence mode counts as one, and so does a process running on the
// GPU.
func TestRemoveGpu_RefusedWhileAttached(t *testing.T) {
	cases := map[string]DeviceConfig{
		"persistence mode": {PersistenceMode: "enabled"},
		"running process":  {Processes: []ProcessConfig{{PID: 4242, Type: "C", Name: "train.py"}}},
	}
	for name, base := range cases {
		t.Run(name, func(t *testing.T) {
			persistSetterWrites(t)
			e := startProcess(t, drainNodeConfig(base, nil))

			require.Equal(t, nvml.ERROR_IN_USE, removeGpu(e, pciAt(t, "0000:0B:00.0")))
			require.Equal(t, drainNodeBDFs, enumerated(t, e))
		})
	}
}

func TestRemoveGpu_RejectsInvalidArguments(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))
	pci := pciAt(t, "0000:0B:00.0")

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.RemoveGpu(pci, nvml.DetachGpuState(2), nvml.PCIE_LINK_KEEP))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.RemoveGpu(pci, nvml.DETACH_GPU_KEEP, nvml.PcieLinkState(2)))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, removeGpu(e, nil))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, removeGpu(e, pciAt(t, "0000:0F:00.0")))
	require.Equal(t, drainNodeBDFs, enumerated(t, e))
}

// TestDiscoverGpus_RestoresEveryRemovedGpu covers a rescan of the whole PCI
// tree. A GPU that comes back has been probed afresh, so it is not draining.
func TestDiscoverGpus_RestoresEveryRemovedGpu(t *testing.T) {
	persistSetterWrites(t)
	cfg := drainNodeConfig(DeviceConfig{}, nil)
	e := startProcess(t, cfg)
	drained := pciAt(t, "0000:0B:00.0")
	require.Equal(t, nvml.SUCCESS, e.ModifyDrainState(drained, nvml.FEATURE_ENABLED))
	require.Equal(t, nvml.SUCCESS, removeGpu(e, drained))
	require.Equal(t, nvml.SUCCESS, removeGpu(e, pciAt(t, "0000:0A:00.0")))

	require.Equal(t, nvml.SUCCESS, e.DiscoverGpus(&nvml.PciInfo{}))

	require.Equal(t, drainNodeBDFs, enumerated(t, e))
	require.Equal(t, drainNodeBDFs, enumerated(t, startProcess(t, cfg)))
	requireDrainState(t, e, "0000:0B:00.0", nvml.FEATURE_DISABLED)
}

func TestDiscoverGpus_NarrowsToAnAddress(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))
	require.Equal(t, nvml.SUCCESS, removeGpu(e, pciAt(t, "0000:0A:00.0")))
	require.Equal(t, nvml.SUCCESS, removeGpu(e, pciAt(t, "0000:0B:00.0")))

	require.Equal(t, nvml.SUCCESS, e.DiscoverGpus(pciAt(t, "0000:0B:00.0")))

	require.Equal(t, []string{"0000:0B:00.0", "0000:0C:00.0"}, enumerated(t, e))
}

func TestDiscoverGpus_RejectsNilAddress(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.DiscoverGpus(nil))
}

func excludeSecondGpu(devs []DeviceOverride) { devs[1].Excluded = true }

// TestExcludedGpu_ListedButNotEnumerated models a GPU the kernel module was
// told to skip: no process enumerates it, and the excluded-device list is how
// a consumer learns it exists.
func TestExcludedGpu_ListedButNotEnumerated(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, excludeSecondGpu))

	require.Equal(t, []string{"0000:0A:00.0", "0000:0C:00.0"}, enumerated(t, e))

	excluded, ret := e.ExcludedDevices()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, excluded, 1)
	require.Equal(t, "00000000:0B:00.0", busIDString(excluded[0].PciInfo.BusId[:]))
	require.Equal(t, uint32(0x0B), excluded[0].PciInfo.Bus)
	require.Equal(t, "GPU-4d4f434b-0000-0000-0000-000000000001", busIDString(excluded[0].Uuid[:]))
}

// TestExcludedGpu_NotManagedByTheDriver: the driver never attached to an
// excluded GPU, so there is nothing to drain or remove, and a rescan does not
// bring it in.
func TestExcludedGpu_NotManagedByTheDriver(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, excludeSecondGpu))
	pci := pciAt(t, "0000:0B:00.0")

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, e.ModifyDrainState(pci, nvml.FEATURE_ENABLED))
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, removeGpu(e, pci))
	require.Equal(t, nvml.SUCCESS, e.DiscoverGpus(&nvml.PciInfo{}))
	require.Equal(t, []string{"0000:0A:00.0", "0000:0C:00.0"}, enumerated(t, e))
}

func TestExcludedDevices_NoneByDefault(t *testing.T) {
	persistSetterWrites(t)
	e := startProcess(t, drainNodeConfig(DeviceConfig{}, nil))

	excluded, ret := e.ExcludedDevices()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, excluded)
}

func TestDrainState_Uninitialized(t *testing.T) {
	e := NewEngine(drainNodeConfig(DeviceConfig{}, nil))
	pci := pciAt(t, "0000:0B:00.0")

	_, ret := e.QueryDrainState(pci)
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret)
	require.Equal(t, nvml.ERROR_UNINITIALIZED, e.ModifyDrainState(pci, nvml.FEATURE_ENABLED))
	require.Equal(t, nvml.ERROR_UNINITIALIZED, removeGpu(e, pci))
	require.Equal(t, nvml.ERROR_UNINITIALIZED, e.DiscoverGpus(pci))
	_, ret = e.ExcludedDevices()
	require.Equal(t, nvml.ERROR_UNINITIALIZED, ret)
}
