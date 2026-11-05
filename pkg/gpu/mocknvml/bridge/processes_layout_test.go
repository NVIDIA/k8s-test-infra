// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Layout tests for the accounting, process-detail and sample structs the
// bridge writes into. See fabric_layout_test.go for why the expected sizes are
// constants rather than C.sizeof_* reads.
//
// Every one of these is an output array or carries a pointer to one, so a
// stride that drifts from the caller's makes the bridge write past the end of
// a buffer go-nvml sized from its own types.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Expected byte sizes derived from nvml_types.h field-by-field. Update in
// lockstep with any C struct change.
//
//	AccountingStats:           gpu(4) + mem(4) + maxMem(8) + time(8) + start(8) + running(4) + reserved(20) = 56
//	AccountingStats_v2:        pid(4) + running(4) + gpu(4) + mem(4) + maxMem(8) + count(4) + pad(4) + 4*8 = 64
//	ProcessInfo (v2 and v3):   pid(4) + pad(4) + mem(8) + gi(4) + ci(4) = 24
//	ProcessDetail_v1:          ProcessInfo(24) + ccProtected(8) = 32
//	ProcessDetailList:         version(4) + mode(4) + count(4) + pad(4) + array(8) = 24
//	ProcessUtilizationInfo_v1: ts(8) + 7*4 + tail pad(4) = 40
//	ProcessesUtilizationInfo:  version(4) + count(4) + lastSeen(8) + array(8) = 24
//	Sample:                    ts(8) + value union(8) = 16
const (
	expectedAccountingStatsSize          uintptr = 56
	expectedAccountingStatsV2Size        uintptr = 64
	expectedProcessInfoSize              uintptr = 24
	expectedProcessDetailSize            uintptr = 32
	expectedProcessDetailListSize        uintptr = 24
	expectedProcessUtilizationInfoSize   uintptr = 40
	expectedProcessesUtilizationInfoSize uintptr = 24
	expectedSampleSize                   uintptr = 16
)

func TestProcessStructLayouts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"AccountingStats", unsafe.Sizeof(nvml.AccountingStats{}), expectedAccountingStatsSize},
		{"AccountingStats_v2", unsafe.Sizeof(nvml.AccountingStats_v2{}), expectedAccountingStatsV2Size},
		{"ProcessInfo", unsafe.Sizeof(nvml.ProcessInfo{}), expectedProcessInfoSize},
		{"ProcessInfo_v2", unsafe.Sizeof(nvml.ProcessInfo_v2{}), expectedProcessInfoSize},
		{"ProcessDetail_v1", unsafe.Sizeof(nvml.ProcessDetail_v1{}), expectedProcessDetailSize},
		{"ProcessDetailList", unsafe.Sizeof(nvml.ProcessDetailList{}), expectedProcessDetailListSize},
		{"ProcessUtilizationInfo_v1", unsafe.Sizeof(nvml.ProcessUtilizationInfo_v1{}), expectedProcessUtilizationInfoSize},
		{"ProcessesUtilizationInfo", unsafe.Sizeof(nvml.ProcessesUtilizationInfo{}), expectedProcessesUtilizationInfoSize},
		{"Sample", unsafe.Sizeof(nvml.Sample{}), expectedSampleSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, tc.goSize,
				"%s: go-nvml = %d bytes, C layout expects %d bytes — ABI drift; update either the go-nvml version or nvml_types.h",
				tc.name, tc.goSize, tc.expected)
		})
	}
}

// TestProcessStructVersions_MatchGoNvml pins the version tags the two
// versioned getters demand against the ones a go-nvml caller stamps; a tag that
// disagrees turns every query into a version refusal.
func TestProcessStructVersions_MatchGoNvml(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		ours   uint32
		theirs uint32
	}{
		{"ProcessDetailList", processDetailListVersion(), nvml.STRUCT_VERSION(nvml.ProcessDetailList{}, 1)},
		{"ProcessesUtilizationInfo", processesUtilizationInfoVersion(), nvml.STRUCT_VERSION(nvml.ProcessesUtilizationInfo{}, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.theirs, tc.ours,
				"bridge demands 0x%x, a go-nvml caller stamps 0x%x — reconcile nvml_types.h with the go-nvml struct",
				tc.ours, tc.theirs)
		})
	}
}
