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
	"encoding/binary"
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// cperAccessTypeGPU is the only record source NVML defines.
const cperAccessTypeGPU = engine.CPERAccessTypeGPU

// =============================================================================
// nvmlSystemGetCPER_v1
// =============================================================================

func TestCPER_NoRecordsReportsZeroBufferSize(t *testing.T) {
	newBridgeEngine(t, profile(driverWithCPER, 2, ""))

	got := cperQueryForTest(cperAccessTypeGPU, "", 0, 4096, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status)
	require.Zero(t, got.bufferSize, "a zero bufferSize on return is NVML's no-records indicator")
	require.Empty(t, got.payload)
}

// TestCPER_SizeQueryThenRead is the two-call sequence a consumer uses: ask how
// much room the records need, allocate, then read them.
func TestCPER_SizeQueryThenRead(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithCPER, 2, ""))
	b.fail(t, "lost")

	sized := cperQueryForTest(cperAccessTypeGPU, "", 0, 0, true)
	require.Equal(t, uint32(nvml.ERROR_INSUFFICIENT_SIZE), sized.status)
	require.NotZero(t, sized.bufferSize, "the size query must report the space needed")
	require.Zero(t, sized.handle, "a size query must not advance the cursor")

	read := cperQueryForTest(cperAccessTypeGPU, "", sized.handle, int(sized.bufferSize), false)
	require.Equal(t, uint32(nvml.SUCCESS), read.status)
	require.Len(t, read.payload, int(sized.bufferSize))
	require.NotZero(t, read.handle, "a read must advance the cursor")
}

// TestCPER_PayloadIsAWellFormedRecord checks what the bridge copied out, not
// what the engine built: a consumer parses these bytes with a UEFI CPER parser.
func TestCPER_PayloadIsAWellFormedRecord(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithCPER, 1, ""))
	b.fail(t, "lost")

	got := cperQueryForTest(cperAccessTypeGPU, "", 0, 4096, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status)
	require.Greater(t, len(got.payload), 128, "a record is at least a 128-byte header")
	require.Equal(t, "CPER", string(got.payload[0:4]))
	require.Equal(t, uint32(len(got.payload)), binary.LittleEndian.Uint32(got.payload[20:24]),
		"recordLength must match what the bridge reported through bufferSize")
	require.Equal(t, uint32(len(got.payload)), got.bufferSize)
}

// TestCPER_CursorWalksToCompletion pins the loop a consumer writes: pass the
// returned handle back until the read comes up empty.
func TestCPER_CursorWalksToCompletion(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithCPER, 3, ""))
	b.fail(t, "lost")

	// One record per call, so the cursor has to be honoured three times.
	one := cperQueryForTest(cperAccessTypeGPU, "", 0, 0, true)
	require.Equal(t, uint32(nvml.ERROR_INSUFFICIENT_SIZE), one.status)
	recordSize := int(one.bufferSize) / 3

	handle := uint64(0)
	records := 0
	for i := 0; i < 5; i++ {
		got := cperQueryForTest(cperAccessTypeGPU, "", handle, recordSize, false)
		require.Equal(t, uint32(nvml.SUCCESS), got.status)
		if got.bufferSize == 0 {
			break
		}
		require.Len(t, got.payload, recordSize, "a record must never be split across calls")
		records++
		handle = got.handle
	}
	require.Equal(t, 3, records, "the walk must yield one record per failed GPU, then stop")
}

// TestCPER_BufferTooSmallDoesNotConsumeTheRecord lets a caller grow its buffer
// and retry with the same cursor, which is the only way out of this return.
func TestCPER_BufferTooSmallDoesNotConsumeTheRecord(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithCPER, 1, ""))
	b.fail(t, "lost")

	got := cperQueryForTest(cperAccessTypeGPU, "", 0, 64, false)
	require.Equal(t, uint32(nvml.ERROR_INSUFFICIENT_SIZE), got.status)
	require.Greater(t, got.bufferSize, uint32(64), "the required size must be reported")
	require.Zero(t, got.handle)

	retry := cperQueryForTest(cperAccessTypeGPU, "", 0, int(got.bufferSize), false)
	require.Equal(t, uint32(nvml.SUCCESS), retry.status)
	require.NotEmpty(t, retry.payload)
}

// TestCPER_UUIDFilterReachesTheEngine covers the cursor's uuid array, which the
// bridge has to read out of a fixed C buffer to build the query.
func TestCPER_UUIDFilterReachesTheEngine(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithCPER, 4, ""))
	uuid := b.uuid(t, 2)
	b.fail(t, "lost")

	filtered := cperQueryForTest(cperAccessTypeGPU, uuid, 0, 4096, false)
	require.Equal(t, uint32(nvml.SUCCESS), filtered.status)
	require.NotEmpty(t, filtered.payload)

	all := cperQueryForTest(cperAccessTypeGPU, "", 0, 4096, false)
	require.Equal(t, uint32(nvml.SUCCESS), all.status)
	require.Len(t, all.payload, 4*len(filtered.payload),
		"the filter must have selected one of the four failed GPUs")
}

func TestCPER_UnknownTypeMaskMatchesNothing(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithCPER, 1, ""))
	b.fail(t, "lost")

	got := cperQueryForTest(0, "", 0, 4096, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status)
	require.Zero(t, got.bufferSize)
}

// TestCPER_RejectsNullBufferWithNonZeroSize separates the documented size
// query (both zero) from a caller that simply passed a bad pointer.
func TestCPER_RejectsNullBufferWithNonZeroSize(t *testing.T) {
	newBridgeEngine(t, profile(driverWithCPER, 1, ""))

	// bufLen 0 with sizeQuery false leaves buffer NULL but bufferSize set.
	got := cperQueryForTest(cperAccessTypeGPU, "", 0, 0, false)
	require.Equal(t, uint32(nvml.SUCCESS), got.status,
		"buffer NULL with bufferSize 0 is the size-query form, not an error")

	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), cperNullBufferForTest(4096),
		"a NULL buffer with a non-zero bufferSize is a bad pointer, not a size query")
}

func TestCPER_FunctionNotFoundOnOlderDriver(t *testing.T) {
	newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))

	got := cperQueryForTest(cperAccessTypeGPU, "", 0, 4096, false)
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND), got.status,
		"no released driver exports nvmlSystemGetCPER_v1; 580.65.06 must not pretend to")
}

// =============================================================================
// nvmlSystemGetDriverBranch
// =============================================================================

func TestDriverBranch_DerivedFromProfile(t *testing.T) {
	newBridgeEngine(t, profile("580.65.06", 1, ""))

	branch, status := driverBranchForTest(80)
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, "r580_00", branch)
}

func TestDriverBranch_ExplicitProfileValue(t *testing.T) {
	newBridgeEngine(t, "version: \"1.0\"\nsystem:\n"+
		"  driver_version: 570.148.08\n"+
		"  num_devices: 1\n"+
		"  driver_branch: r570_40\n")

	branch, status := driverBranchForTest(80)
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, "r570_40", branch)
}

// TestDriverBranch_InsufficientLength covers the caller that declares a
// shorter limit than the branch needs.
func TestDriverBranch_InsufficientLength(t *testing.T) {
	newBridgeEngine(t, profile("580.65.06", 1, ""))

	_, status := driverBranchForTest(4)
	require.Equal(t, uint32(nvml.ERROR_INSUFFICIENT_SIZE), status,
		"r580_00 needs eight bytes with its terminator")
}

// TestDriverBranch_OverlongLengthIsClamped is the bounds check that matters:
// length is the caller's claim, but the destination is the fixed array inside
// the struct it handed us.
func TestDriverBranch_OverlongLengthIsClamped(t *testing.T) {
	newBridgeEngine(t, profile("580.65.06", 1, ""))

	branch, status := driverBranchForTest(1 << 20)
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, "r580_00", branch)
}

func TestDriverBranch_FunctionNotFoundOnOlderDriver(t *testing.T) {
	newBridgeEngine(t, profile("470.256.02", 1, ""))

	_, status := driverBranchForTest(80)
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND), status)
}

// =============================================================================
// nvmlSystemGetHicVersion
// =============================================================================

// TestHicVersion_NoneConfigured is what `nvidia-smi -q -u` sees on every
// modern node, and the reason the export exists at all: nvidia-smi is the one
// consumer of the pinned driver that calls it.
func TestHicVersion_NoneConfigured(t *testing.T) {
	newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))

	entries, reported, status := hicVersionForTest(4)
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Zero(t, reported)
	require.Empty(t, entries)
}

// TestUnitGetCount_IsZeroAndSucceeds is what makes the HIC block reachable:
// `nvidia-smi -q -u` reads the unit count first and aborts with "Unable to
// determine number of available units" if it fails, taking the HIC block down
// with it.
func TestUnitGetCount_IsZeroAndSucceeds(t *testing.T) {
	newBridgeEngine(t, profile(driverWithSystemEvents, 2, ""))

	count, status := unitCountForTest(false)
	require.Equal(t, uint32(nvml.SUCCESS), status,
		"no S-class enclosure is not an error; zero units is the honest answer")
	require.Zero(t, count)

	_, status = unitCountForTest(true)
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), status)
}

func TestHicVersion_ReportsConfiguredCards(t *testing.T) {
	newBridgeEngine(t, "version: \"1.0\"\nsystem:\n"+
		"  driver_version: "+driverWithSystemEvents+"\n"+
		"  num_devices: 1\n"+
		"  hic:\n"+
		"    - id: 0\n      firmware_version: 1.2.3.4\n"+
		"    - id: 3\n      firmware_version: 5.6.7.8\n")

	entries, reported, status := hicVersionForTest(4)
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, uint32(2), reported)
	require.Equal(t, []engine.HICEntry{
		{ID: 0, FirmwareVersion: "1.2.3.4"},
		{ID: 3, FirmwareVersion: "5.6.7.8"},
	}, entries)
}

// TestHicVersion_InsufficientCapacityReportsCount is the grow-and-retry
// protocol go-nvml's wrapper implements.
func TestHicVersion_InsufficientCapacityReportsCount(t *testing.T) {
	newBridgeEngine(t, "version: \"1.0\"\nsystem:\n"+
		"  driver_version: "+driverWithSystemEvents+"\n"+
		"  num_devices: 1\n"+
		"  hic:\n"+
		"    - id: 0\n      firmware_version: 1.2.3.4\n"+
		"    - id: 1\n      firmware_version: 5.6.7.8\n")

	_, reported, status := hicVersionForTest(1)
	require.Equal(t, uint32(nvml.ERROR_INSUFFICIENT_SIZE), status)
	require.Equal(t, uint32(2), reported, "the caller needs the real count to grow its array")

	entries, reported, status := hicVersionForTest(int(reported))
	require.Equal(t, uint32(nvml.SUCCESS), status)
	require.Equal(t, uint32(2), reported)
	require.Len(t, entries, 2)
}

func TestHicVersion_FunctionNotFoundOnOlderDriver(t *testing.T) {
	newBridgeEngine(t, profile("300.0", 1, ""))

	_, _, status := hicVersionForTest(4)
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND), status)
}

// =============================================================================
// goStringFromC
// =============================================================================

// TestGoStringFromC covers the helper the CPER cursor and the hostname setter
// read their fixed C arrays through. C.GoString would run past the end of an
// array a caller filled completely, which is legal for these NVML buffers.
func TestGoStringFromC(t *testing.T) {
	t.Parallel()
	require.Equal(t, "GPU-0", goStringFromCForTest("GPU-0", 64, true))
	require.Empty(t, goStringFromCForTest("", 64, true))

	full := strings.Repeat("a", 8)
	require.Equal(t, full, goStringFromCForTest(full, 8, false),
		"an array filled to its last byte must be read whole, not overrun")
}
