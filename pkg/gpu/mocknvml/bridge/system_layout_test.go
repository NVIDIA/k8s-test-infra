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

// Layout tests for the system event, CPER, HIC, driver branch and hostname
// structs the bridge reads and writes.
//
// Same approach and rationale as fabric_layout_test.go: Go forbids cgo in test
// files for a package carrying //export directives, so the expected sizes are
// hard-coded from the field-by-field layout in nvml_types.h. A go-nvml bump
// that moves a field would otherwise have the bridge quietly writing the wrong
// bytes into a caller's struct.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// Expected byte sizes derived from nvml_types.h, accounting for natural
// alignment on a 64-bit target. Update in lockstep with any C struct change.
const (
	// handle pointer only.
	expectedSystemEventSetSize uintptr = 8
	// version(4) + 4 pad + set(8).
	expectedSystemEventSetCreateRequestSize uintptr = 16
	expectedSystemEventSetFreeRequestSize   uintptr = 16
	// version(4) + 4 pad + eventTypes(8) + set(8).
	expectedSystemRegisterEventRequestSize uintptr = 24
	// eventType(8) + gpuId(4) + 4 trailing pad.
	expectedSystemEventDataSize uintptr = 16
	// version(4) + timeoutms(4) + set(8) + data(8) + dataSize(4) + numEvent(4).
	expectedSystemEventSetWaitRequestSize uintptr = 32
	// cperTypeMask(4) + uuid[80] + 4 pad + handle(8).
	expectedCPERCursorSize uintptr = 96
	// cursor(96) + buffer(8) + bufferSize(4) + 4 trailing pad.
	expectedGetCPERSize uintptr = 112
	// hwbcId(4) + firmwareVersion[32].
	expectedHwbcEntrySize uintptr = 36
	// version(4) + branch[80].
	expectedSystemDriverBranchInfoSize uintptr = 84
	// value[64].
	expectedHostnameSize uintptr = 64
)

func TestSystemStructLayouts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		goSize   uintptr
		expected uintptr
	}{
		{"SystemEventSet", unsafe.Sizeof(nvml.SystemEventSet{}), expectedSystemEventSetSize},
		{"SystemEventSetCreateRequest", unsafe.Sizeof(nvml.SystemEventSetCreateRequest{}), expectedSystemEventSetCreateRequestSize},
		{"SystemEventSetFreeRequest", unsafe.Sizeof(nvml.SystemEventSetFreeRequest{}), expectedSystemEventSetFreeRequestSize},
		{"SystemRegisterEventRequest", unsafe.Sizeof(nvml.SystemRegisterEventRequest{}), expectedSystemRegisterEventRequestSize},
		{"SystemEventData_v1", unsafe.Sizeof(nvml.SystemEventData_v1{}), expectedSystemEventDataSize},
		{"SystemEventSetWaitRequest", unsafe.Sizeof(nvml.SystemEventSetWaitRequest{}), expectedSystemEventSetWaitRequestSize},
		{"CPERCursor_v1", unsafe.Sizeof(nvml.CPERCursor_v1{}), expectedCPERCursorSize},
		{"GetCPER_v1", unsafe.Sizeof(nvml.GetCPER_v1{}), expectedGetCPERSize},
		{"HwbcEntry", unsafe.Sizeof(nvml.HwbcEntry{}), expectedHwbcEntrySize},
		{"SystemDriverBranchInfo", unsafe.Sizeof(nvml.SystemDriverBranchInfo{}), expectedSystemDriverBranchInfoSize},
		{"Hostname_v1", unsafe.Sizeof(nvml.Hostname_v1{}), expectedHostnameSize},
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

// TestSystemEventTypes_MatchGoNvml pins the values the engine hands back in
// nvmlSystemEventData_v1_t.eventType. A consumer filters on these, so a
// swapped bind and unbind would look like a working event set delivering the
// opposite of what happened.
func TestSystemEventTypes_MatchGoNvml(t *testing.T) {
	t.Parallel()
	require.Equal(t, engine.SystemEventTypeGpuDriverUnbind, uint64(nvml.SystemEventTypeGpuDriverUnbind))
	require.Equal(t, engine.SystemEventTypeGpuDriverBind, uint64(nvml.SystemEventTypeGpuDriverBind))
	require.Equal(t, engine.CPERAccessTypeGPU, uint32(nvml.CPER_ACCESS_TYPE_GPU))
}

// TestSystemBufferSizes_MatchGoNvml pins the string limits the engine
// truncates at against the C arrays they have to fit, with room for the NUL.
func TestSystemBufferSizes_MatchGoNvml(t *testing.T) {
	t.Parallel()
	require.Equal(t, nvml.DEVICE_HOSTNAME_BUFFER_SIZE-1, engine.HostnameMaxLen)
	require.Equal(t, len(nvml.HwbcEntry{}.FirmwareVersion)-1, engine.HICFirmwareVersionMaxLen)
	require.Len(t, nvml.SystemDriverBranchInfo{}.Branch, nvml.SYSTEM_DRIVER_VERSION_BUFFER_SIZE)
}
