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

package main

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// The internal export-table process entry is not a public NVML struct: its
// numeric header matches nvmlProcessDetail_v1_t but is followed by an inline
// 4096-byte name buffer, giving a 4128-byte stride. These values were recovered
// by probing the real nvidia-smi 580.65.06 and are load-bearing -- a wrong
// stride silently renders every process after the first as PID 0 / [N/A] / 0
// MiB, and omitting the inline name makes nvidia-smi drop the rows from the
// default table's Processes box entirely.
func TestProcessEntryLayout_MatchesNvidiaSMIExpectations(t *testing.T) {
	require.Equal(t, 32, procEntryNameOffset)
	require.Equal(t, 4096, procEntryNameMax)
	require.Equal(t, 4128, procEntrySize)
}

func TestWriteProcessEntry_StrideAndFields(t *testing.T) {
	const n = 3
	buf := make([]byte, n*procEntrySize)
	base := unsafe.Pointer(&buf[0])

	writeProcessEntry(base, 0, 100, 8192*1024*1024, "train.py")
	writeProcessEntry(base, 1, 200, 512*1024*1024, "infer.py")
	writeProcessEntry(base, 2, 300, 64*1024*1024, "jupyter")

	for i, want := range []struct {
		pid  uint32
		mem  uint64
		name string
	}{
		{100, 8192 * 1024 * 1024, "train.py"},
		{200, 512 * 1024 * 1024, "infer.py"},
		{300, 64 * 1024 * 1024, "jupyter"},
	} {
		e := unsafe.Add(base, i*procEntrySize)
		require.Equal(t, want.pid, *(*uint32)(e), "entry %d pid", i)
		require.Equal(t, want.mem, *(*uint64)(unsafe.Add(e, 8)), "entry %d usedGpuMemory", i)
		require.Equal(t, uint32(0xFFFFFFFF), *(*uint32)(unsafe.Add(e, 16)), "entry %d gpuInstanceId", i)
		require.Equal(t, uint32(0xFFFFFFFF), *(*uint32)(unsafe.Add(e, 20)), "entry %d computeInstanceId", i)

		nameBuf := unsafe.Slice((*byte)(unsafe.Add(e, procEntryNameOffset)), procEntryNameMax)
		got, _, found := bytes.Cut(nameBuf, []byte{0})
		require.True(t, found, "entry %d name must be NUL-terminated", i)
		require.Equal(t, want.name, string(got), "entry %d name", i)
	}
}

func TestWriteProcessEntry_TruncatesOverlongName(t *testing.T) {
	buf := make([]byte, 2*procEntrySize)
	base := unsafe.Pointer(&buf[0])

	long := bytes.Repeat([]byte("x"), procEntryNameMax*2)
	writeProcessEntry(base, 0, 42, 7, string(long))
	// A second entry guards against the truncated name running past its own
	// buffer and corrupting the next one.
	writeProcessEntry(base, 1, 43, 8, "next")

	nameBuf := unsafe.Slice((*byte)(unsafe.Add(base, procEntryNameOffset)), procEntryNameMax)
	got, _, found := bytes.Cut(nameBuf, []byte{0})
	require.True(t, found, "truncated name must still be NUL-terminated")
	require.Len(t, got, procEntryNameMax-1)

	second := unsafe.Add(base, procEntrySize)
	require.Equal(t, uint32(43), *(*uint32)(second))
	secondName := unsafe.Slice((*byte)(unsafe.Add(second, procEntryNameOffset)), procEntryNameMax)
	gotSecond, _, _ := bytes.Cut(secondName, []byte{0})
	require.Equal(t, "next", string(gotSecond))
}
