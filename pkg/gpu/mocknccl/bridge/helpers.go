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

// Package main builds the mock libnccl.so.2 via buildmode=c-shared.
package main

/*
#include <stdlib.h>
#include <string.h>
#include "nccl_types.h"
*/
import "C"
import (
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {}

// elementSize maps an ncclDataType_t to its byte width.
func elementSize(dt C.ncclDataType_t) int64 {
	switch dt {
	case C.ncclInt8, C.ncclUint8:
		return 1
	case C.ncclFloat16, C.ncclBfloat16:
		return 2
	case C.ncclInt32, C.ncclUint32, C.ncclFloat32:
		return 4
	case C.ncclInt64, C.ncclUint64, C.ncclFloat64:
		return 8
	default:
		return 4
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func maxSleep() time.Duration {
	return time.Duration(envInt("MOCK_NCCL_MAX_SLEEP_MS", 50)) * time.Millisecond
}

// errStr returns a cached C string for an ncclResult_t (callers must not free).
var (
	errMu    sync.Mutex
	errCache = map[C.ncclResult_t]*C.char{}
)

func errStr(r C.ncclResult_t) *C.char {
	errMu.Lock()
	defer errMu.Unlock()
	if s, ok := errCache[r]; ok {
		return s
	}
	msg := "unknown result"
	switch r {
	case C.ncclSuccess:
		msg = "no error"
	case C.ncclInvalidArgument:
		msg = "invalid argument"
	case C.ncclSystemError:
		msg = "unhandled system error"
	case C.ncclInternalError:
		msg = "internal error"
	}
	cs := C.CString(msg)
	errCache[r] = cs
	return cs
}
