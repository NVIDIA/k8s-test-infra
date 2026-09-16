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

// Package main provides the node-scoped RAS and identity queries:
// nvmlSystemGetCPER_v1, nvmlSystemGetDriverBranch and nvmlSystemGetHicVersion.
package main

/*
#include <stdlib.h>
#include <string.h>
#include "nvml_types.h"
*/
import "C"

import (
	"bytes"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// nvmlSystemGetCPER_v1 returns the Common Platform Error Records the mock has
// synthesised from injected RAS state. Records are genuine UEFI CPER: a
// 128-byte record header, one section descriptor, and either the standard
// Memory Error Section (uncorrectable ECC) or the mock's own GPU section. The
// header always carries the Hardware Error Record "simulated" flag, so a
// consumer can tell mock telemetry from a real fault.
//
// The cursor contract is NVML's: pass the same cursor on every call of a
// sequence, and reset handle to NVML_CPER_CURSOR_HANDLE_INIT to restart or to
// change the type mask or UUID filter.
//
//export nvmlSystemGetCPER_v1
func nvmlSystemGetCPER_v1(cper *C.nvmlGetCPER_v1_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemGetCPER_v1"); !ok {
		return ret
	}
	if cper == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	bufferSize := uint32(cper.bufferSize)
	if cper.buffer == nil && bufferSize != 0 {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	// buffer NULL with bufferSize 0 is the documented size query.
	sizeQuery := cper.buffer == nil && bufferSize == 0

	query := engine.CPERQuery{
		TypeMask: uint32(cper.cursor.cperTypeMask),
		UUID:     goStringFromC(&cper.cursor.uuid[0], C.NVML_DEVICE_UUID_BUFFER_SIZE),
		Handle:   uint64(cper.cursor.handle),
	}
	result, ret := engine.GetEngine().SystemGetCPER(query, bufferSize, sizeQuery)
	cper.cursor.handle = C.nvmlCPERCursorHandle_t(result.NextHandle)
	if ret != nvml.SUCCESS {
		cper.bufferSize = C.uint(result.Required)
		return toReturn(ret)
	}
	if len(result.Payload) > 0 {
		C.memcpy(unsafe.Pointer(cper.buffer), unsafe.Pointer(&result.Payload[0]), C.size_t(len(result.Payload)))
	}
	// A zero bufferSize on return is NVML's "no (more) records" indicator.
	cper.bufferSize = C.uint(len(result.Payload))
	return C.NVML_SUCCESS
}

//export nvmlSystemGetDriverBranch
func nvmlSystemGetDriverBranch(branchInfo *C.nvmlSystemDriverBranchInfo_t, length C.uint) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemGetDriverBranch"); !ok {
		return ret
	}
	if branchInfo == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	branch, ret := engine.GetEngine().SystemGetDriverBranch()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	// length is the caller's declared limit, but the destination is the fixed
	// array inside the struct it handed us: clamp so an over-large length can
	// never authorise a write past it.
	if length > C.NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE {
		length = C.NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE
	}
	return goStringToC(branch, &branchInfo.branch[0], length)
}

// nvmlSystemGetHicVersion reports the node's host interface cards, which
// nvidia-smi renders as the `hic_info` block of `nvidia-smi -q -u`. A node with
// none — every modern DGX/HGX/NVL system, since HICs belong to the retired
// S-class enclosures — answers SUCCESS with a count of zero, the only "no
// cards" answer NVML's return set allows.
//
//export nvmlSystemGetHicVersion
func nvmlSystemGetHicVersion(hwbcCount *C.uint, hwbcEntries *C.nvmlHwbcEntry_t) C.nvmlReturn_t {
	if ret, ok := bridgeVersionCheck("nvmlSystemGetHicVersion"); !ok {
		return ret
	}
	if hwbcCount == nil || hwbcEntries == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	entries, ret := engine.GetEngine().SystemGetHicVersion()
	if ret != nvml.SUCCESS {
		return toReturn(ret)
	}
	if len(entries) > int(*hwbcCount) {
		// Report the size needed; go-nvml's SystemGetHicVersion grows its array
		// and retries on this return.
		*hwbcCount = C.uint(len(entries))
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}

	out := unsafe.Slice(hwbcEntries, int(*hwbcCount))
	for i, entry := range entries {
		out[i].hwbcId = C.uint(entry.ID)
		if ret := goStringToC(entry.FirmwareVersion, &out[i].firmwareVersion[0],
			C.uint(len(out[i].firmwareVersion))); ret != C.NVML_SUCCESS {
			return ret
		}
	}
	*hwbcCount = C.uint(len(entries))
	return C.NVML_SUCCESS
}

// goStringFromC reads a NUL-terminated C string that may legitimately fill its
// whole fixed-size array. C.GoString would run past the end of an unterminated
// buffer, which a caller filling all NVML_DEVICE_UUID_BUFFER_SIZE bytes
// produces.
func goStringFromC(buf *C.char, size int) string {
	if buf == nil || size <= 0 {
		return ""
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(buf)), size)
	if end := bytes.IndexByte(raw, 0); end >= 0 {
		return string(raw[:end])
	}
	return string(raw)
}

// Test hooks. A _test.go file cannot name the C structs these exports take, so
// these build them the way go-nvml does and drive the real entry points.

// cperQueryResult is what the bridge wrote back into nvmlGetCPER_v1_t.
type cperQueryResult struct {
	status     uint32
	handle     uint64
	bufferSize uint32
	payload    []byte
}

// cperQueryForTest issues one nvmlSystemGetCPER_v1 call. sizeQuery selects the
// buffer==NULL / bufferSize==0 form; otherwise bufLen bytes are allocated.
func cperQueryForTest(typeMask uint32, uuid string, handle uint64, bufLen int, sizeQuery bool) cperQueryResult {
	var request C.nvmlGetCPER_v1_t
	request.cursor.cperTypeMask = C.uint(typeMask)
	request.cursor.handle = C.nvmlCPERCursorHandle_t(handle)
	if uuid != "" {
		goStringToC(uuid, &request.cursor.uuid[0], C.uint(len(request.cursor.uuid)))
	}
	if !sizeQuery && bufLen > 0 {
		buffer := C.calloc(C.size_t(bufLen), 1)
		if buffer == nil {
			return cperQueryResult{status: C.NVML_ERROR_MEMORY}
		}
		defer C.free(buffer)
		request.buffer = (*C.uchar)(buffer)
		request.bufferSize = C.uint(bufLen)
	}

	out := cperQueryResult{status: uint32(nvmlSystemGetCPER_v1(&request))}
	out.handle = uint64(request.cursor.handle)
	out.bufferSize = uint32(request.bufferSize)
	if request.buffer != nil && out.bufferSize > 0 {
		out.payload = C.GoBytes(unsafe.Pointer(request.buffer), C.int(out.bufferSize))
	}
	return out
}

// cperNullBufferForTest passes a NULL buffer alongside a non-zero bufferSize,
// the one combination that is a bad pointer rather than the documented size
// query. cperQueryForTest cannot express it: it derives both from bufLen.
func cperNullBufferForTest(bufferSize uint32) uint32 {
	var request C.nvmlGetCPER_v1_t
	request.cursor.cperTypeMask = C.uint(engine.CPERAccessTypeGPU)
	request.bufferSize = C.uint(bufferSize)
	return uint32(nvmlSystemGetCPER_v1(&request))
}

func driverBranchForTest(length uint32) (string, uint32) {
	var info C.nvmlSystemDriverBranchInfo_t
	status := uint32(nvmlSystemGetDriverBranch(&info, C.uint(length)))
	return goStringFromC(&info.branch[0], len(info.branch)), status
}

// hicVersionForTest allocates room for capacity entries and reports what the
// export wrote back, including the count it demands on INSUFFICIENT_SIZE.
func hicVersionForTest(capacity int) (entries []engine.HICEntry, reported uint32, status uint32) {
	count := C.uint(capacity)
	slots := capacity
	if slots < 1 {
		// nvmlSystemGetHicVersion rejects a NULL entries array outright, so a
		// zero-capacity probe still has to pass one.
		slots = 1
	}
	raw := C.calloc(C.size_t(slots), C.size_t(unsafe.Sizeof(C.nvmlHwbcEntry_t{})))
	if raw == nil {
		return nil, 0, C.NVML_ERROR_MEMORY
	}
	defer C.free(raw)

	status = uint32(nvmlSystemGetHicVersion(&count, (*C.nvmlHwbcEntry_t)(raw)))
	reported = uint32(count)
	if status != C.NVML_SUCCESS {
		return nil, reported, status
	}
	written := unsafe.Slice((*C.nvmlHwbcEntry_t)(raw), slots)
	for i := 0; i < int(reported) && i < slots; i++ {
		entries = append(entries, engine.HICEntry{
			ID:              uint32(written[i].hwbcId),
			FirmwareVersion: goStringFromC(&written[i].firmwareVersion[0], len(written[i].firmwareVersion)),
		})
	}
	return entries, reported, status
}

// goStringFromCForTest round-trips a Go string through a fixed-size C buffer so
// the unterminated-array path goStringFromC guards is exercised directly.
// terminate == false fills the whole buffer with no NUL.
func goStringFromCForTest(s string, size int, terminate bool) string {
	buffer := C.calloc(C.size_t(size), 1)
	defer C.free(buffer)
	raw := unsafe.Slice((*byte)(buffer), size)
	copy(raw, s)
	if !terminate {
		for i := len(s); i < size; i++ {
			raw[i] = 'x'
		}
	}
	return goStringFromC((*C.char)(buffer), size)
}
