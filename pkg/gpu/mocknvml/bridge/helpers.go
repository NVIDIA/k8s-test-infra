// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

// Package main provides helper functions for the mock NVML bridge.
// This file contains:
// - main() function required for buildmode=c-shared
// - Type conversion helpers (toReturn, goStringToC)
// - Stub handling (stubReturn)
// - Error string caching for nvmlErrorString

//go:generate go run ../../../../cmd/generate-bridge/main.go -input ../../../../vendor/github.com/NVIDIA/go-nvml/pkg/nvml/nvml.go -bridge . -output stubs_generated.go

package main

/*
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Include NVML type definitions for strict ABI compatibility.
#include "nvml_types.h"
*/
import "C"
import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// main is required for buildmode=c-shared.
// All actual functionality is in exported functions.
func main() {
	// Required for buildmode=c-shared
}

// errorCache manages cached C strings for nvmlErrorString.
// Real NVML returns static strings that callers must NOT free.
// We cache allocated strings forever to match this behavior.
type errorCache struct {
	mu sync.Mutex
	m  map[nvml.Return]*C.char
}

var errStrings = &errorCache{
	m: make(map[nvml.Return]*C.char),
}

// get returns a cached C string for the error code.
// MEMORY CONTRACT: Returned strings are cached until Clear() is called.
// This matches real NVML behavior where nvmlErrorString returns
// pointers to static strings that callers must NOT free.
func (c *errorCache) get(ret nvml.Return) *C.char {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.m[ret]; ok {
		return cached
	}

	str := nvml.ErrorString(ret)
	cStr := C.CString(str)
	c.m[ret] = cStr
	return cStr
}

// clear frees all cached C strings and resets the cache.
// Call this during shutdown to prevent memory leaks.
func (c *errorCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, cStr := range c.m {
		C.free(unsafe.Pointer(cStr))
	}
	c.m = make(map[nvml.Return]*C.char)
}

// ClearErrorStringCache frees all cached error strings.
// Exported for use by the bridge layer during shutdown.
func ClearErrorStringCache() {
	errStrings.clear()
}

// debugEnabled controls whether debug messages are printed
var debugEnabled = os.Getenv("MOCK_NVML_DEBUG") != ""

// strictMode panics when unimplemented functions are called.
// Enable with MOCK_NVML_STRICT=1 for debugging which stubs are hit.
var strictMode = os.Getenv("MOCK_NVML_STRICT") != ""

// debugLog prints to stderr only if MOCK_NVML_DEBUG is set
func debugLog(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

// toReturn converts Go nvml.Return to C nvmlReturn_t
func toReturn(ret nvml.Return) C.nvmlReturn_t {
	return C.nvmlReturn_t(ret)
}

// goStringToC copies a Go string to a C buffer with bounds checking.
// Returns NVML_ERROR_INVALID_ARGUMENT if buf is nil.
// Returns NVML_ERROR_INSUFFICIENT_SIZE if the string doesn't fit.
func goStringToC(s string, buf *C.char, length C.uint) C.nvmlReturn_t {
	if buf == nil {
		return C.NVML_ERROR_INVALID_ARGUMENT
	}
	if len(s)+1 > int(length) {
		return C.NVML_ERROR_INSUFFICIENT_SIZE
	}
	cStr := C.CString(s)
	defer C.free(unsafe.Pointer(cStr))
	C.strcpy(buf, cStr)
	return C.NVML_SUCCESS
}

// stubReturn handles unimplemented function calls.
// In strict mode, panics to help identify missing implementations.
// Otherwise returns NOT_SUPPORTED (gracefully handled by most consumers).
func stubReturn(funcName string) C.nvmlReturn_t {
	if strictMode {
		panic(fmt.Sprintf("MOCK_NVML_STRICT: unimplemented function called: %s", funcName))
	}
	debugLog("[NVML-STUB] %s called (NOT IMPLEMENTED)\n", funcName)
	return C.NVML_ERROR_NOT_SUPPORTED
}

//export nvmlErrorString
func nvmlErrorString(result C.nvmlReturn_t) *C.char {
	return errStrings.get(nvml.Return(result))
}
