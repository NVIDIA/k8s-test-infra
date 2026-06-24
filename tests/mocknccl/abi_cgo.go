//go:build integration

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

// Package main holds the cgo bridge for the libnccl.so.2 ABI smoke test. Go
// forbids `import "C"` inside *_test.go files, so the cgo preamble and thin Go
// wrappers live here and are exercised by abi_test.go.
package main

/*
#cgo LDFLAGS: -L${SRCDIR}/../../pkg/gpu/mocknccl -lnccl -Wl,-rpath,${SRCDIR}/../../pkg/gpu/mocknccl
#include <stdlib.h>
#include "../../pkg/gpu/mocknccl/bridge/nccl_types.h"
extern int ncclGetVersion(int*);
extern int ncclCommInitAll(ncclComm_t*, int, int*);
extern int ncclAllReduce(void*, void*, size_t, ncclDataType_t, ncclRedOp_t, ncclComm_t, cudaStream_t);
extern int ncclCommDestroy(ncclComm_t);
*/
import "C"
import "unsafe"

func getVersion() (rc int, version int) {
	var ver C.int
	rc = int(C.ncclGetVersion(&ver))
	return rc, int(ver)
}

func commInitAll(n int) (rc int, comms []unsafe.Pointer) {
	cComms := make([]C.ncclComm_t, n)
	rc = int(C.ncclCommInitAll(&cComms[0], C.int(n), nil))
	comms = make([]unsafe.Pointer, n)
	for i := range cComms {
		comms[i] = unsafe.Pointer(cComms[i])
	}
	return rc, comms
}

func allReduce(comm unsafe.Pointer) int {
	return int(C.ncclAllReduce(nil, nil, C.size_t(1024), C.ncclFloat32, C.ncclSum,
		C.ncclComm_t(comm), (C.cudaStream_t)(unsafe.Pointer(nil))))
}

func commDestroy(comm unsafe.Pointer) int {
	return int(C.ncclCommDestroy(C.ncclComm_t(comm)))
}
