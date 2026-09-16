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
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// gpuCPERQuery is the cursor a client opens an iteration with.
func gpuCPERQuery() CPERQuery {
	return CPERQuery{TypeMask: CPERAccessTypeGPU}
}

// =============================================================================
// Record encoding
// =============================================================================

// TestEncodeCPERRecord_RecordHeader walks the UEFI record header field by
// field. A consumer locates the section table from these offsets, so a wrong
// one makes the whole buffer unparseable rather than merely inaccurate.
func TestEncodeCPERRecord_RecordHeader(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.September, 15, 20, 7, 42, 0, time.UTC)
	record := encodeCPERRecord(cperFaultSource{
		UUID:  "GPU-4d4f434b-0000-0000-0000-000000000003",
		GpuID: 0x400,
		Mode:  FailureModeFallenOffBus,
		Xid:   79,
	}, 7, at)

	require.Len(t, record, cperHeaderSize+cperSectionSize+cperGPUSectionSize)
	require.Equal(t, "CPER", string(record[0:4]))
	require.Equal(t, uint16(cperRecordRevision), binary.LittleEndian.Uint16(record[4:6]))
	require.Equal(t, uint32(cperSignatureEnd), binary.LittleEndian.Uint32(record[6:10]))
	require.Equal(t, uint16(1), binary.LittleEndian.Uint16(record[10:12]), "one section per record")
	require.Equal(t, uint32(cperSeverityFatal), binary.LittleEndian.Uint32(record[12:16]))
	require.Equal(t, uint32(cperValidTimestamp), binary.LittleEndian.Uint32(record[16:20]),
		"only the timestamp is marked valid; the mock fills in no platform or partition id")
	require.Equal(t, uint32(len(record)), binary.LittleEndian.Uint32(record[20:24]),
		"record length must cover header, section descriptor and body")
	require.Equal(t, uint64(7), binary.LittleEndian.Uint64(record[96:104]))
	require.Equal(t, uint32(cperFlagSimulated), binary.LittleEndian.Uint32(record[104:108]),
		"a simulator must flag its records as simulated")

	// Platform and partition id stay zero because their validation bits are clear.
	require.Equal(t, make([]byte, 32), record[32:64])
}

func TestEncodeCPERRecord_TimestampIsBCD(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.September, 15, 20, 7, 42, 0, time.UTC)
	record := encodeCPERRecord(cperFaultSource{Mode: FailureModeLost}, 1, at)

	timestamp := record[24:32]
	require.Equal(t, byte(0x42), timestamp[0], "seconds")
	require.Equal(t, byte(0x07), timestamp[1], "minutes")
	require.Equal(t, byte(0x20), timestamp[2], "hours")
	require.Equal(t, byte(0x01), timestamp[3], "precise flag")
	require.Equal(t, byte(0x15), timestamp[4], "day")
	require.Equal(t, byte(0x09), timestamp[5], "month")
	require.Equal(t, byte(0x26), timestamp[6], "year within century")
	require.Equal(t, byte(0x20), timestamp[7], "century")
}

func TestEncodeCPERRecord_SectionDescriptor(t *testing.T) {
	t.Parallel()
	record := encodeCPERRecord(cperFaultSource{Mode: FailureModeLost}, 1, time.Unix(0, 0))
	section := record[cperHeaderSize : cperHeaderSize+cperSectionSize]

	require.Equal(t, uint32(cperHeaderSize+cperSectionSize), binary.LittleEndian.Uint32(section[0:4]),
		"the body starts immediately after the single section descriptor")
	require.Equal(t, uint32(cperGPUSectionSize), binary.LittleEndian.Uint32(section[4:8]))
	require.Equal(t, uint16(cperSectionRevision), binary.LittleEndian.Uint16(section[8:10]))
	require.Equal(t, byte(0), section[10], "no FRU id and no FRU string")
	require.Equal(t, uint32(cperSectionFlagPrimary), binary.LittleEndian.Uint32(section[12:16]))
	require.Equal(t, uint32(cperSeverityFatal), binary.LittleEndian.Uint32(section[48:52]))
}

// TestEncodeCPERRecord_SectionTypePerFault pins the choice of container: an
// uncorrectable ECC error is a real UEFI memory error and uses the standard
// section, everything else uses the mock's own GPU section.
func TestEncodeCPERRecord_SectionTypePerFault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode     string
		guid     cperGUID
		bodySize int
	}{
		{FailureModeECCUncorrectable, cperMemorySectionGUID, cperMemorySectionSize},
		{FailureModeLost, cperGPUSectionGUID, cperGPUSectionSize},
		{FailureModeFallenOffBus, cperGPUSectionGUID, cperGPUSectionSize},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			record := encodeCPERRecord(cperFaultSource{Mode: tc.mode}, 1, time.Unix(0, 0))
			section := record[cperHeaderSize : cperHeaderSize+cperSectionSize]

			want := make([]byte, 16)
			tc.guid.encode(want)
			require.Equal(t, want, section[16:32])
			require.Equal(t, uint32(tc.bodySize), binary.LittleEndian.Uint32(section[4:8]))
		})
	}
}

// TestEncodeCPERRecord_GPUSectionBody covers the layout documented on
// encodeCPERGPUSection, which is the only way a consumer recovers the Xid.
func TestEncodeCPERRecord_GPUSectionBody(t *testing.T) {
	t.Parallel()
	uuid := "GPU-4d4f434b-0000-0000-0000-000000000005"
	record := encodeCPERRecord(cperFaultSource{
		UUID:  uuid,
		GpuID: 0x600,
		Mode:  FailureModeFallenOffBus,
		Xid:   74,
	}, 1, time.Unix(0, 0))
	body := record[cperHeaderSize+cperSectionSize:]

	require.Equal(t, uint64(74), binary.LittleEndian.Uint64(body[0:8]))
	require.Equal(t, uint32(0x600), binary.LittleEndian.Uint32(body[8:12]))
	require.Equal(t, uint32(2), binary.LittleEndian.Uint32(body[12:16]), "fallen_off_bus")
	require.Equal(t, uuid, string(body[16:16+len(uuid)]))
	require.Equal(t, byte(0), body[16+len(uuid)], "the UUID must stay NUL terminated")
}

func TestEncodeCPERRecord_MemorySectionBody(t *testing.T) {
	t.Parallel()
	record := encodeCPERRecord(cperFaultSource{Mode: FailureModeECCUncorrectable}, 1, time.Unix(0, 0))
	body := record[cperHeaderSize+cperSectionSize:]

	require.Len(t, body, cperMemorySectionSize)
	require.Equal(t, uint64(cperMemoryValidErrorStatus|cperMemoryValidErrorType),
		binary.LittleEndian.Uint64(body[0:8]),
		"the address, bank, row and column of a simulated error would be fiction and stay invalid")
	require.Equal(t, uint64(cperMemoryUncorrectedError), binary.LittleEndian.Uint64(body[8:16]))
	require.Equal(t, byte(cperMemoryErrorTypeMultiBitECC), body[cperMemoryErrorTypeOffset])
}

// TestEncodeCPERRecord_OverlongUUIDIsTruncated guards the fixed body: a UUID
// longer than the field must not run past it.
func TestEncodeCPERRecord_OverlongUUIDIsTruncated(t *testing.T) {
	t.Parallel()
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'A'
	}
	record := encodeCPERRecord(cperFaultSource{UUID: string(long), Mode: FailureModeLost}, 1, time.Unix(0, 0))
	body := record[cperHeaderSize+cperSectionSize:]
	require.Len(t, body, cperGPUSectionSize)
	require.Equal(t, byte(0), body[len(body)-1], "the truncated UUID must keep its terminator")
}

// =============================================================================
// Cursor protocol
// =============================================================================

func TestEngineCPER_NoRecordsIsSuccessWithZeroSize(t *testing.T) {
	e, _, _ := newSystemEventEngine(t, 2)
	result, ret := e.SystemGetCPER(gpuCPERQuery(), 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, result.Payload, "a healthy node has no records")
	require.Zero(t, result.Required)
	require.Zero(t, result.NextHandle, "an empty read must not advance the cursor")
}

func TestEngineCPER_TypeMaskWithoutGPUMatchesNothing(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: ecc_uncorrectable\n", clock)

	result, ret := e.SystemGetCPER(CPERQuery{TypeMask: 0}, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, result.Payload, "NVML defines no record source other than GPU")
}

func TestEngineCPER_SizeQueryReportsTotalWithoutAdvancing(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 2)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: lost\n", clock)

	result, ret := e.SystemGetCPER(gpuCPERQuery(), 0, true)
	require.Equal(t, nvml.ERROR_INSUFFICIENT_SIZE, ret)
	require.Equal(t, uint32(2*(cperHeaderSize+cperSectionSize+cperGPUSectionSize)), result.Required)
	require.Zero(t, result.NextHandle, "a size query must leave the cursor where it was")

	// The records are still there for the read that follows.
	read, ret := e.SystemGetCPER(gpuCPERQuery(), result.Required, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, read.Payload, int(result.Required))
}

func TestEngineCPER_BufferTooSmallForOneRecord(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: lost\n", clock)

	recordSize := uint32(cperHeaderSize + cperSectionSize + cperGPUSectionSize)
	result, ret := e.SystemGetCPER(gpuCPERQuery(), recordSize-1, false)
	require.Equal(t, nvml.ERROR_INSUFFICIENT_SIZE, ret)
	require.Equal(t, recordSize, result.Required)
	require.Zero(t, result.NextHandle, "a rejected read must not consume the record")

	retry, ret := e.SystemGetCPER(gpuCPERQuery(), recordSize, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, retry.Payload, int(recordSize))
}

// TestEngineCPER_IterationSplitsOnWholeRecords is the core of the cursor
// contract: a partial record would leave the caller with an unparseable tail.
func TestEngineCPER_IterationSplitsOnWholeRecords(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 3)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: lost\n", clock)

	recordSize := uint32(cperHeaderSize + cperSectionSize + cperGPUSectionSize)
	query := gpuCPERQuery()

	// A buffer sized for two records must deliver exactly two.
	first, ret := e.SystemGetCPER(query, 2*recordSize+recordSize/2, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, first.Payload, int(2*recordSize))
	require.NotZero(t, first.NextHandle)

	query.Handle = first.NextHandle
	second, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, second.Payload, int(recordSize), "the third record must arrive on the next call")

	query.Handle = second.NextHandle
	third, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, third.Payload, "the iteration must terminate")
}

// TestEngineCPER_ResetHandleRestartsIteration covers the documented way to
// change the filter: rewind the handle to NVML_CPER_CURSOR_HANDLE_INIT.
func TestEngineCPER_ResetHandleRestartsIteration(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 2)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: lost\n", clock)

	query := gpuCPERQuery()
	drained, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotEmpty(t, drained.Payload)

	query.Handle = 0
	again, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, drained.Payload, again.Payload)
}

func TestEngineCPER_UUIDFilterSelectsOneGPU(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 4)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: lost\n", clock)

	wanted := fmt.Sprintf("GPU-4d4f434b-0000-0000-0000-%012d", 2)
	query := gpuCPERQuery()
	query.UUID = wanted
	result, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, result.Payload, cperHeaderSize+cperSectionSize+cperGPUSectionSize,
		"the filter must select exactly the one GPU")

	body := result.Payload[cperHeaderSize+cperSectionSize:]
	require.Equal(t, deviceGpuID(2), binary.LittleEndian.Uint32(body[8:12]))
}

// TestEngineCPER_OneRecordPerOccurrence is why the log tracks what it has
// already recorded: a consumer polling in a loop must not watch the journal
// grow by one record per call while the GPU sits in the same failed state.
func TestEngineCPER_OneRecordPerOccurrence(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: ecc_uncorrectable\n", clock)

	for i := 0; i < 4; i++ {
		result, ret := e.SystemGetCPER(gpuCPERQuery(), 0, true)
		require.Equal(t, nvml.ERROR_INSUFFICIENT_SIZE, ret)
		require.Equal(t, uint32(cperHeaderSize+cperSectionSize+cperMemorySectionSize), result.Required,
			"a device that stays tripped must not accrue a record per query")
	}
}

// TestEngineCPER_HealedDeviceCanFaultAgain is the other half: once the fault
// is cleared, re-injecting it is a genuinely new occurrence and earns a record.
func TestEngineCPER_HealedDeviceCanFaultAgain(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: ecc_uncorrectable\n", clock)

	query := gpuCPERQuery()
	first, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotEmpty(t, first.Payload)

	require.NoError(t, os.Remove(path))
	*clock = clock.Add(2 * time.Second)
	query.Handle = first.NextHandle
	healed, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, healed.Payload, "healing produces no record of its own")

	writeConfigOverride(t, path, "all:\n  failure:\n    mode: ecc_uncorrectable\n", clock)
	query.Handle = healed.NextHandle
	again, ret := e.SystemGetCPER(query, 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, again.Payload, cperHeaderSize+cperSectionSize+cperMemorySectionSize)
}

// TestEngineCPER_XidTravelsWithTheRecord pins that reading CPER does not steal
// the Xid from nvmlEventSetWait: the RAS log and the event set are independent
// surfaces and both must report the same fault.
func TestEngineCPER_XidTravelsWithTheRecord(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	writeConfigOverride(t, path,
		"all:\n  failure:\n    mode: ecc_uncorrectable\n    xid:\n      code: 48\n", clock)

	result, ret := e.SystemGetCPER(gpuCPERQuery(), 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.NotEmpty(t, result.Payload, "an uncorrectable ECC error belongs in the RAS log")

	// Trip the injector the way an ECC consumer would, with a guarded getter.
	_, ret = deviceByIndex(t, e, 0).GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC)
	require.Equal(t, nvml.SUCCESS, ret)

	_, xid, ok := e.PendingXidEvent()
	require.True(t, ok, "reading the RAS log must not consume the event-set delivery")
	require.Equal(t, uint64(48), xid)
}

// TestEngineCPER_DeclaredFaultDoesNotWaitForAGuardedCall covers the client
// that only reads the RAS log. ecc_uncorrectable returns SUCCESS from every
// guarded getter, so nothing else would ever trip the injector for it.
func TestEngineCPER_DeclaredFaultDoesNotWaitForAGuardedCall(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	writeConfigOverride(t, path, "all:\n  failure:\n    mode: ecc_uncorrectable\n", clock)

	result, ret := e.SystemGetCPER(gpuCPERQuery(), 4096, false)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, result.Payload, cperHeaderSize+cperSectionSize+cperMemorySectionSize)
}

// TestEngineCPER_AfterCallsFaultIsNotRecordedEarly is the other side of that
// leniency: a fault gated behind after_calls has not happened yet, and reading
// the log must not be what makes it happen.
func TestEngineCPER_AfterCallsFaultIsNotRecordedEarly(t *testing.T) {
	e, path, clock := newSystemEventEngine(t, 1)
	writeConfigOverride(t, path,
		"all:\n  failure:\n    mode: ecc_uncorrectable\n    after_calls: 50\n", clock)

	for i := 0; i < 5; i++ {
		result, ret := e.SystemGetCPER(gpuCPERQuery(), 4096, false)
		require.Equal(t, nvml.SUCCESS, ret)
		require.Empty(t, result.Payload)
	}
	require.Zero(t, deviceByIndex(t, e, 0).failureInjector().CallCount(),
		"a read-only surface must not advance the after_calls countdown")
}

// deviceByIndex resolves the configurable device behind an enumeration index.
func deviceByIndex(t *testing.T, e *Engine, index int) *ConfigurableDevice {
	t.Helper()
	handle, ret := e.DeviceGetHandleByIndex(index)
	require.Equal(t, nvml.SUCCESS, ret)
	dev, ok := e.LookupDevice(handle).(*ConfigurableDevice)
	require.True(t, ok)
	return dev
}

func TestEngineCPER_LogDropsOldestPastCap(t *testing.T) {
	t.Parallel()
	log := newCPERLog()
	log.now = func() time.Time { return time.Unix(0, 0) }

	for i := 0; i < maxCPERRecords+10; i++ {
		log.observe(map[int]cperFaultSource{
			0: {UUID: "GPU-0", Mode: FailureModeLost, Xid: uint64(i)},
		})
	}
	require.Len(t, log.records, maxCPERRecords)
	require.Equal(t, uint64(maxCPERRecords+10), log.records[len(log.records)-1].seq,
		"sequence numbers must stay monotonic so a live cursor never repeats a record")
}
