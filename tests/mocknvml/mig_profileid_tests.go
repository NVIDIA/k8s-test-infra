// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// GPU instance profile IDs across the C ABI.
//
// NVML gives a profile two numbers and uses both: a caller walks the profiles
// by enum, then names one to create by the ID the board reports for it. The ID
// encodes the partition's share of the board, so the two orderings run
// opposite — on an A100 the 1-slice profile is enum 0 but ID 19, while ID 0 is
// the whole board.
//
// These are the numbers `nvidia-smi mig -cgi <id>` takes, so a mock reporting
// the enum in their place partitions differently from hardware under the same
// command. Nothing else in this suite pins them: the rest reads an ID back out
// of the mock and hands the same value in again, which round-trips whatever
// numbering it happens to use.

package main

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// The IDs the A100-SXM4-40GB fixture must report, from the
// `nvidia-smi mig -lgip` listing in NVIDIA's MIG user guide.
var a100ProfileIDs = []struct {
	profileEnum int
	wantID      uint32
	wantSlices  uint32
}{
	{nvml.GPU_INSTANCE_PROFILE_1_SLICE, 19, 1},
	{nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1, 20, 1},
	{nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2, 15, 1},
	{nvml.GPU_INSTANCE_PROFILE_2_SLICE, 14, 2},
	{nvml.GPU_INSTANCE_PROFILE_3_SLICE, 9, 3},
	{nvml.GPU_INSTANCE_PROFILE_4_SLICE, 5, 4},
	{nvml.GPU_INSTANCE_PROFILE_7_SLICE, 0, 7},
}

// testMIGProfileIDs drives the profile-ID surface on device 4, which the
// fixture leaves MIG-off, free of running processes and unused by the other
// MIG legs — so toggling its mode here cannot perturb another test.
func testMIGProfileIDs(index int) []testResult {
	name := "mig/profileid/gpu4"

	device, ret := nvml.DeviceGetHandleByIndex(index)
	if ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("GetHandleByIndex: %v", nvml.ErrorString(ret))}}
	}
	if ret, _ := device.SetMigMode(nvml.DEVICE_MIG_ENABLE); ret != nvml.SUCCESS {
		return []testResult{{name, false, fmt.Sprintf("SetMigMode(enable): %v", nvml.ErrorString(ret))}}
	}
	// Leave the device as the fixture had it, so test order cannot matter.
	defer device.SetMigMode(nvml.DEVICE_MIG_DISABLE) //nolint:errcheck // best-effort cleanup

	results := []testResult{
		checkReportedProfileIDs(name+"/reported", device),
		checkCreateByReportedID(name+"/create-by-id", device),
		checkWholeBoardIsIDZero(name+"/id-zero-is-whole-board", device),
		checkUnpublishedIDRefused(name+"/unpublished-id", device),
	}
	return results
}

// checkReportedProfileIDs walks the profiles by enum and pins the ID each one
// publishes, which is the column `nvidia-smi mig -lgip` prints.
func checkReportedProfileIDs(name string, device nvml.Device) testResult {
	for _, want := range a100ProfileIDs {
		info, ret := device.GetGpuInstanceProfileInfo(want.profileEnum)
		if ret != nvml.SUCCESS {
			return testResult{name, false, fmt.Sprintf(
				"GetGpuInstanceProfileInfo(enum %d): %v", want.profileEnum, nvml.ErrorString(ret))}
		}
		if info.Id != want.wantID {
			return testResult{name, false, fmt.Sprintf(
				"profile enum %d reports id %d; want %d (the id nvidia-smi mig -cgi takes)",
				want.profileEnum, info.Id, want.wantID)}
		}
		if info.SliceCount != want.wantSlices {
			return testResult{name, false, fmt.Sprintf(
				"profile id %d spans %d slices; want %d",
				info.Id, info.SliceCount, want.wantSlices)}
		}
	}
	return testResult{name, true, ""}
}

// checkCreateByReportedID creates by the published ID and confirms the
// partition that appears is the one that ID names, then confirms the instance
// reports the same ID back rather than the enum it is stamped with.
func checkCreateByReportedID(name string, device nvml.Device) testResult {
	// 3g.20gb: a middling profile, so an off-by-one in either direction of the
	// mapping lands on a different slice count.
	info, ret := device.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_3_SLICE)
	if ret != nvml.SUCCESS {
		return testResult{name, false, fmt.Sprintf("GetGpuInstanceProfileInfo: %v", nvml.ErrorString(ret))}
	}
	if info.Id != 9 {
		return testResult{name, false, fmt.Sprintf("the 3-slice profile reports id %d; want 9", info.Id)}
	}

	gi, ret := device.CreateGpuInstance(&info)
	if ret != nvml.SUCCESS {
		return testResult{name, false, fmt.Sprintf("CreateGpuInstance(id 9): %v", nvml.ErrorString(ret))}
	}
	defer gi.Destroy() //nolint:errcheck // best-effort cleanup

	giInfo, ret := gi.GetInfo()
	if ret != nvml.SUCCESS {
		return testResult{name, false, fmt.Sprintf("GpuInstance.GetInfo: %v", nvml.ErrorString(ret))}
	}
	if giInfo.ProfileId != 9 {
		return testResult{name, false, fmt.Sprintf(
			"the instance created from id 9 reports profile %d; want 9", giInfo.ProfileId)}
	}
	// Placement size, not slice count: NVML pads placements to power-of-two
	// boundaries, so a 3-slice instance covers four grid slots.
	if giInfo.Placement.Size != 4 {
		return testResult{name, false, fmt.Sprintf(
			"the instance created from id 9 covers %d slots; want 4, as a 3-slice partition does",
			giInfo.Placement.Size)}
	}
	return testResult{name, true, ""}
}

// checkWholeBoardIsIDZero pins the inversion that makes this worth testing: on
// hardware ID 0 hands over the entire board, where the enum of the same value
// is the smallest partition it offers.
func checkWholeBoardIsIDZero(name string, device nvml.Device) testResult {
	info, ret := device.GetGpuInstanceProfileInfo(nvml.GPU_INSTANCE_PROFILE_7_SLICE)
	if ret != nvml.SUCCESS {
		return testResult{name, false, fmt.Sprintf("GetGpuInstanceProfileInfo: %v", nvml.ErrorString(ret))}
	}
	if info.Id != 0 {
		return testResult{name, false, fmt.Sprintf("the 7-slice profile reports id %d; want 0", info.Id)}
	}
	if info.SliceCount != 7 {
		return testResult{name, false, fmt.Sprintf(
			"id 0 spans %d slices; want 7, the whole board", info.SliceCount)}
	}

	remaining, ret := device.GetGpuInstanceRemainingCapacity(&info)
	if ret != nvml.SUCCESS {
		return testResult{name, false, fmt.Sprintf(
			"GetGpuInstanceRemainingCapacity(id 0): %v", nvml.ErrorString(ret))}
	}
	if remaining != 1 {
		return testResult{name, false, fmt.Sprintf(
			"an empty board fits %d instances of id 0; want 1", remaining)}
	}
	return testResult{name, true, ""}
}

// checkUnpublishedIDRefused asserts an ID the board publishes no profile under
// is an error. Resolving it to the profile whose enum happens to match would
// create a partition hardware would have refused.
func checkUnpublishedIDRefused(name string, device nvml.Device) testResult {
	// 3 is the enum of the 4-slice profile but is published by nothing: on this
	// board the 4-slice profile answers to 5.
	const unpublished = 3
	info := nvml.GpuInstanceProfileInfo{Id: unpublished}
	if _, ret := device.CreateGpuInstance(&info); ret == nvml.SUCCESS {
		return testResult{name, false, fmt.Sprintf(
			"CreateGpuInstance(id %d) succeeded; want a refusal, as no profile publishes that id",
			unpublished)}
	}
	if _, ret := device.GetGpuInstancePossiblePlacements(&info); ret == nvml.SUCCESS {
		return testResult{name, false, fmt.Sprintf(
			"GetGpuInstancePossiblePlacements(id %d) succeeded; want a refusal", unpublished)}
	}
	return testResult{name, true, ""}
}
