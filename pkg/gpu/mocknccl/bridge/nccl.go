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

/*
#include <stdlib.h>
#include <string.h>
#include "nccl_types.h"
*/
import "C"
import (
	"context"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknccl/engine"
)

// commRegistry maps C ncclComm_t handles (real C pointers) to engine comms.
var (
	commMu  sync.Mutex
	comms   = map[uintptr]*engine.Comm{}
	cfgOnce sync.Once
	cfg     engine.Config
)

func loadCfg() engine.Config {
	cfgOnce.Do(func() {
		cfg = engine.LoadConfig(os.Getenv("MOCK_NCCL_CONFIG"))
	})
	return cfg
}

//export ncclGetVersion
func ncclGetVersion(version *C.int) C.ncclResult_t {
	if version == nil {
		return C.ncclInvalidArgument
	}
	*version = C.int(loadCfg().Version)
	return C.ncclSuccess
}

//export ncclGetUniqueId
func ncclGetUniqueId(out *C.ncclUniqueId) C.ncclResult_t {
	if out == nil {
		return C.ncclInvalidArgument
	}
	// The id is opaque in our MPI-free flow (rank/world come from env); zero it.
	C.memset(unsafe.Pointer(out), 0, C.size_t(C.NCCL_UNIQUE_ID_BYTES))
	return C.ncclSuccess
}

//export ncclGetErrorString
func ncclGetErrorString(result C.ncclResult_t) *C.char {
	return errStr(result)
}

// ncclGetLastError intentionally ignores comm and always reports success
// (mock simplification, consistent with the mockcuda bridge).
//
//export ncclGetLastError
func ncclGetLastError(comm C.ncclComm_t) *C.char {
	return errStr(C.ncclSuccess)
}

// ncclCommInitRank forms the comm. Rank/world come from the args (the driver
// reads RANK/WORLD_SIZE); the rendezvous barrier confirms cross-pod liveness
// and selects intra/inter-node scope.
//
//export ncclCommInitRank
func ncclCommInitRank(comm *C.ncclComm_t, nranks C.int, commId C.ncclUniqueId, rank C.int) C.ncclResult_t {
	if comm == nil {
		return C.ncclInvalidArgument
	}
	c, rc := buildComm(int(rank), int(nranks))
	if rc != C.ncclSuccess {
		return rc
	}
	h := registerComm(c)
	if h == nil {
		return C.ncclSystemError
	}
	*comm = C.ncclComm_t(h)
	return C.ncclSuccess
}

// ncclCommInitAll forms nGpus single-process comms (no rendezvous).
//
//export ncclCommInitAll
func ncclCommInitAll(comms_ *C.ncclComm_t, ndev C.int, devlist *C.int) C.ncclResult_t {
	if comms_ == nil || ndev <= 0 {
		return C.ncclInvalidArgument
	}
	conf := loadCfg()
	out := unsafe.Slice(comms_, int(ndev))
	for i := 0; i < int(ndev); i++ {
		c := &engine.Comm{Rank: i, WorldSize: int(ndev), InterNode: false,
			Model: conf.Model(), MaxSleep: maxSleep()}
		h := registerComm(c)
		if h == nil {
			return C.ncclSystemError
		}
		out[i] = C.ncclComm_t(h)
	}
	return C.ncclSuccess
}

//export ncclCommDestroy
func ncclCommDestroy(comm C.ncclComm_t) C.ncclResult_t {
	key := uintptr(unsafe.Pointer(comm))
	commMu.Lock()
	_, ok := comms[key]
	delete(comms, key)
	commMu.Unlock()
	if ok {
		C.free(unsafe.Pointer(comm))
	}
	return C.ncclSuccess
}

//export ncclCommAbort
func ncclCommAbort(comm C.ncclComm_t) C.ncclResult_t {
	return ncclCommDestroy(comm)
}

//export ncclCommCount
func ncclCommCount(comm C.ncclComm_t, count *C.int) C.ncclResult_t {
	c := lookupComm(comm)
	if c == nil || count == nil {
		return C.ncclInvalidArgument
	}
	*count = C.int(c.WorldSize)
	return C.ncclSuccess
}

//export ncclCommUserRank
func ncclCommUserRank(comm C.ncclComm_t, rank *C.int) C.ncclResult_t {
	c := lookupComm(comm)
	if c == nil || rank == nil {
		return C.ncclInvalidArgument
	}
	*rank = C.int(c.Rank)
	return C.ncclSuccess
}

//export ncclCommCuDevice
func ncclCommCuDevice(comm C.ncclComm_t, device *C.int) C.ncclResult_t {
	c := lookupComm(comm)
	if c == nil || device == nil {
		return C.ncclInvalidArgument
	}
	*device = C.int(c.Rank)
	return C.ncclSuccess
}

//export ncclGroupStart
func ncclGroupStart() C.ncclResult_t { return C.ncclSuccess }

//export ncclGroupEnd
func ncclGroupEnd() C.ncclResult_t { return C.ncclSuccess }

// --- collectives: all delegate to runColl with the right element count ---

//export ncclAllReduce
func ncclAllReduce(sendbuff, recvbuff unsafe.Pointer, count C.size_t, datatype C.ncclDataType_t, op C.ncclRedOp_t, comm C.ncclComm_t, stream C.cudaStream_t) C.ncclResult_t {
	return runColl(engine.AllReduce, comm, int64(count), datatype)
}

//export ncclAllGather
func ncclAllGather(sendbuff, recvbuff unsafe.Pointer, sendcount C.size_t, datatype C.ncclDataType_t, comm C.ncclComm_t, stream C.cudaStream_t) C.ncclResult_t {
	return runColl(engine.AllGather, comm, int64(sendcount), datatype)
}

//export ncclReduceScatter
func ncclReduceScatter(sendbuff, recvbuff unsafe.Pointer, recvcount C.size_t, datatype C.ncclDataType_t, op C.ncclRedOp_t, comm C.ncclComm_t, stream C.cudaStream_t) C.ncclResult_t {
	return runColl(engine.ReduceScatter, comm, int64(recvcount), datatype)
}

//export ncclBroadcast
func ncclBroadcast(sendbuff, recvbuff unsafe.Pointer, count C.size_t, datatype C.ncclDataType_t, root C.int, comm C.ncclComm_t, stream C.cudaStream_t) C.ncclResult_t {
	return runColl(engine.Broadcast, comm, int64(count), datatype)
}

//export ncclBcast
func ncclBcast(buff unsafe.Pointer, count C.size_t, datatype C.ncclDataType_t, root C.int, comm C.ncclComm_t, stream C.cudaStream_t) C.ncclResult_t {
	return runColl(engine.Broadcast, comm, int64(count), datatype)
}

//export ncclReduce
func ncclReduce(sendbuff, recvbuff unsafe.Pointer, count C.size_t, datatype C.ncclDataType_t, op C.ncclRedOp_t, root C.int, comm C.ncclComm_t, stream C.cudaStream_t) C.ncclResult_t {
	return runColl(engine.Reduce, comm, int64(count), datatype)
}

// --- internal helpers ---

func runColl(op engine.Collective, comm C.ncclComm_t, count int64, dt C.ncclDataType_t) C.ncclResult_t {
	c := lookupComm(comm)
	if c == nil {
		return C.ncclInvalidArgument
	}
	c.RunCollective(op, count*elementSize(dt))
	return C.ncclSuccess
}

func buildComm(rank, nranks int) (*engine.Comm, C.ncclResult_t) {
	conf := loadCfg()
	interNode := false
	if nranks > 1 {
		rdzv := os.Getenv("MOCK_NCCL_RDZV")
		self := os.Getenv("POD_IP")
		if self == "" {
			self = "rank-" + os.Getenv("RANK")
		}
		if rdzv != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			res, err := engine.Rendezvous(ctx, rank, nranks, rdzv, self)
			if err != nil {
				return nil, C.ncclSystemError
			}
			interNode = res.InterNode
		} else {
			interNode = true // multi-rank without local rendezvous => assume cross-node
		}
	}
	return &engine.Comm{Rank: rank, WorldSize: nranks, InterNode: interNode,
		Model: conf.Model(), MaxSleep: maxSleep()}, C.ncclSuccess
}

// registerComm backs the opaque handle with real C memory (vet-clean, mirrors
// cudaMalloc) keyed by the real pointer's uintptr; freed in ncclCommDestroy.
func registerComm(c *engine.Comm) unsafe.Pointer {
	h := C.malloc(C.size_t(1))
	if h == nil {
		return nil
	}
	commMu.Lock()
	defer commMu.Unlock()
	comms[uintptr(h)] = c
	return h
}

func lookupComm(comm C.ncclComm_t) *engine.Comm {
	commMu.Lock()
	defer commMu.Unlock()
	return comms[uintptr(unsafe.Pointer(comm))]
}
