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
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// TestErrorString_IsStatic pins the memory contract of nvmlErrorString: real
// NVML hands out pointers to static strings, so the same code must return the
// same address every time and the bytes must outlive nvmlShutdown.
//
// This is not theoretical. go-nvml wraps the pointer zero-copy, so every error
// string a consumer had already read turned to garbage when the cache was
// freed at shutdown — including statuses a caller prints after tearing NVML
// down, which is the normal shape of a one-shot NVML client.
func TestErrorString_IsStatic(t *testing.T) {
	b := newBridgeEngine(t, profile(driverWithSystemEvents, 1, ""))

	code := uint32(nvml.ERROR_FUNCTION_NOT_FOUND)
	first, text := errorStringForTest(code)
	require.Equal(t, "ERROR_FUNCTION_NOT_FOUND", text)

	again, text := errorStringForTest(code)
	require.Equal(t, first, again, "nvmlErrorString must return one static pointer per code")
	require.Equal(t, "ERROR_FUNCTION_NOT_FOUND", text)

	require.Equal(t, uint32(nvml.SUCCESS), shutdownForTest())

	afterShutdown, text := errorStringForTest(code)
	require.Equal(t, first, afterShutdown, "the error string must survive nvmlShutdown")
	require.Equal(t, "ERROR_FUNCTION_NOT_FOUND", text)

	// Restore the refcount the fixture's own teardown expects to release.
	require.Equal(t, nvml.SUCCESS, b.Init())
}
