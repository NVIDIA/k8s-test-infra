// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// testDrainLifecycle walks the `nvidia-smi drain` sequence an operator runs
// against one GPU — persistence off, drain, remove, rediscover — through the
// built library. Every call takes the caller's nvmlPciInfo_t rather than a
// handle, so this is what proves the bridge reads that struct at the right
// offsets; it also resolves both RemoveGpu symbols, since go-nvml's
// DeviceRemoveGpu binds the unversioned one.
//
// GPU 1 is used because the fixture gives it no processes, which would
// otherwise hold it attached. The lifecycle ends with the GPU rediscovered and
// its persistence mode restored, so it runs last.
func testDrainLifecycle(deviceCount int) []testResult {
	const name = "drain/gpu1"
	if deviceCount < 2 {
		return []testResult{skippedResult(name, "needs two GPUs")}
	}
	device, ret := nvml.DeviceGetHandleByIndex(1)
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetHandleByIndex(1): %v", nvml.ErrorString(ret))}}
	}
	pci, ret := device.GetPciInfo()
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetPciInfo: %v", nvml.ErrorString(ret))}}
	}
	persistence, ret := device.GetPersistenceMode()
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetPersistenceMode: %v", nvml.ErrorString(ret))}}
	}

	var results []testResult
	check := func(step string, ok bool, detail string, args ...any) {
		if ok {
			results = append(results, testResult{name + "/" + step, true, ""})
			return
		}
		results = append(results, testResult{name + "/" + step, false, fmt.Sprintf(detail, args...)})
	}
	drainState := func() nvml.EnableState {
		state, ret := nvml.DeviceQueryDrainState(&pci)
		if ret != nvml.SUCCESS {
			check("query", false, "QueryDrainState: %v", nvml.ErrorString(ret))
		}
		return state
	}
	count := func() int {
		n, _ := nvml.DeviceGetCount()
		return n
	}

	check("initially_not_draining", drainState() == nvml.FEATURE_DISABLED, "a fresh GPU reports draining")

	if persistence == nvml.FEATURE_ENABLED {
		ret := nvml.DeviceModifyDrainState(&pci, nvml.FEATURE_ENABLED)
		check("refused_while_persistent", ret == nvml.ERROR_IN_USE,
			"drain with persistence on returned %v, want IN_USE", nvml.ErrorString(ret))
		ret = device.SetPersistenceMode(nvml.FEATURE_DISABLED)
		check("persistence_off", ret == nvml.SUCCESS, "SetPersistenceMode(0): %v", nvml.ErrorString(ret))
	}

	ret = nvml.DeviceModifyDrainState(&pci, nvml.FEATURE_ENABLED)
	check("drain", ret == nvml.SUCCESS && drainState() == nvml.FEATURE_ENABLED,
		"ModifyDrainState(1) returned %v and did not read back as draining", nvml.ErrorString(ret))

	removals := []struct {
		label  string
		remove func() nvml.Return
	}{
		{"remove_legacy", func() nvml.Return { return nvml.DeviceRemoveGpu(&pci) }},
		{"remove_v2", func() nvml.Return {
			return nvml.DeviceRemoveGpu_v2(&pci, nvml.DETACH_GPU_REMOVE, nvml.PCIE_LINK_SHUT_DOWN)
		}},
	}
	for _, r := range removals {
		ret := r.remove()
		check(r.label, ret == nvml.SUCCESS && count() == deviceCount-1,
			"returned %v with %d GPUs enumerated, want SUCCESS and %d", nvml.ErrorString(ret), count(), deviceCount-1)
		_, ret = nvml.DeviceDiscoverGpus()
		check(r.label+"/rediscover", ret == nvml.SUCCESS && count() == deviceCount,
			"DiscoverGpus returned %v with %d GPUs enumerated, want SUCCESS and %d",
			nvml.ErrorString(ret), count(), deviceCount)
	}
	check("rediscovered_not_draining", drainState() == nvml.FEATURE_DISABLED,
		"a rediscovered GPU still reports draining")

	excluded, ret := nvml.GetExcludedDeviceCount()
	check("excluded_count", ret == nvml.SUCCESS && excluded == 0,
		"GetExcludedDeviceCount returned %d, %v; want 0, SUCCESS", excluded, nvml.ErrorString(ret))
	_, ret = nvml.GetExcludedDeviceInfoByIndex(0)
	check("excluded_index_out_of_range", ret == nvml.ERROR_INVALID_ARGUMENT,
		"GetExcludedDeviceInfoByIndex(0) returned %v, want INVALID_ARGUMENT", nvml.ErrorString(ret))

	if ret := device.SetPersistenceMode(persistence); ret != nvml.SUCCESS {
		check("restore_persistence", false, "SetPersistenceMode(%d): %v", persistence, nvml.ErrorString(ret))
	}
	return results
}
