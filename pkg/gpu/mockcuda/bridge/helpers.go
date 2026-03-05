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
#include "cuda_types.h"
*/
import "C"
import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockcuda/engine"
)

// main is required for buildmode=c-shared.
func main() {}

var debugEnabled = os.Getenv("MOCK_CUDA_DEBUG") != ""

func debugLog(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

// toCudaError converts engine error to C error code.
func toCudaError(err engine.CudaError) C.cudaError_t {
	return C.cudaError_t(err)
}

// errorStringCache caches C strings for cudaGetErrorString.
// Real CUDA returns static strings that callers must NOT free.
type errorStringCache struct {
	mu sync.Mutex
	m  map[engine.CudaError]*C.char
}

var errStrings = &errorStringCache{
	m: make(map[engine.CudaError]*C.char),
}

func (c *errorStringCache) get(err engine.CudaError) *C.char {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.m[err]; ok {
		return cached
	}
	str := engine.ErrorString(err)
	cStr := C.CString(str)
	c.m[err] = cStr
	return cStr
}

func (c *errorStringCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cStr := range c.m {
		C.free(unsafe.Pointer(cStr))
	}
	c.m = make(map[engine.CudaError]*C.char)
}
