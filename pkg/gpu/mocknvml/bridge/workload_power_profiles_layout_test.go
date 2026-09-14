// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Layout tests for the workload power profile structs the bridge writes into.
// See fabric_layout_test.go for why the expected sizes are constants rather
// than C.sizeof_* reads.
//
// These sizes matter twice over, as with the SRAM status: the bridge writes a
// 255-entry array into a caller-allocated buffer, and each size is half of the
// version tag the caller must present, so drift turns every
// `nvidia-smi power-profiles` query into a version mismatch.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Expected byte sizes derived from nvml_types.h field by field.
//
//	Mask255:                   8 * 4 = 32
//	WorkloadPowerProfileInfo:  version(4) + profileId(4) + priority(4)
//	                           + conflictingMask(32) = 44
//	ProfilesInfo:              version(4) + perfProfilesMask(32)
//	                           + 255 * 44 = 11256
//	CurrentProfiles:           version(4) + 3 * 32 = 100
const (
	expectedMask255Size                 uintptr = 32
	expectedWorkloadProfileInfoSize     uintptr = 44
	expectedWorkloadProfilesInfoSize    uintptr = 11256
	expectedWorkloadCurrentProfilesSize uintptr = 100
)

func TestWorkloadPowerProfileStructLayouts(t *testing.T) {
	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"Mask255", unsafe.Sizeof(nvml.Mask255{}), expectedMask255Size},
		{
			"WorkloadPowerProfileInfo",
			unsafe.Sizeof(nvml.WorkloadPowerProfileInfo{}),
			expectedWorkloadProfileInfoSize,
		},
		{
			"WorkloadPowerProfileProfilesInfo",
			unsafe.Sizeof(nvml.WorkloadPowerProfileProfilesInfo{}),
			expectedWorkloadProfilesInfoSize,
		},
		{
			"WorkloadPowerProfileCurrentProfiles",
			unsafe.Sizeof(nvml.WorkloadPowerProfileCurrentProfiles{}),
			expectedWorkloadCurrentProfilesSize,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, tc.goSize,
				"%s: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either "+
					"the go-nvml version or nvml_types.h", tc.name, tc.goSize, tc.expected)
		})
	}
}

// TestWorkloadPowerProfileVersions_MatchGoNvml pins the tags the bridge demands
// against the ones a caller stamping with go-nvml's STRUCT_VERSION produces.
// nvidia-smi fills these in before calling; if the two disagree, every
// `nvidia-smi power-profiles` query answers ARGUMENT_VERSION_MISMATCH.
//
// Calling the production functions is what makes this a real pin: they read
// sizeof of the C structs, so the C layout sits on one side of the comparison
// and go-nvml's on the other.
func TestWorkloadPowerProfileVersions_MatchGoNvml(t *testing.T) {
	cases := []struct {
		name   string
		theirs uint32
		ours   uint32
	}{
		{
			"ProfilesInfo",
			nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileProfilesInfo_v1{}, 1),
			workloadProfilesInfoVersion(),
		},
		{
			"CurrentProfiles",
			nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileCurrentProfiles_v1{}, 1),
			workloadCurrentProfilesVersion(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.theirs, tc.ours,
				"bridge demands 0x%x, a go-nvml caller stamps 0x%x; reconcile nvml_types.h "+
					"with the go-nvml struct", tc.ours, tc.theirs)
		})
	}
}
