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

//go:build !nopadding

// Library-size padding for the mock libnvidia-ml.so.
//
// The real libnvidia-ml.so shipped by the NVIDIA driver is roughly 14 MiB on
// disk (driver 550.x). Some detection / security tools sanity-check the file
// size of the loaded NVML library, so the mock embeds a configurable padding
// blob that lands in a dedicated `.data` section.
//
// The size is controlled at build time via CGO_CFLAGS:
//
//	CGO_CFLAGS='-DNVML_MOCK_PADDING_BYTES=14680064' go build ...
//
// Padding can be disabled entirely with the `nopadding` build tag, which
// excludes this file from compilation and produces a small library suitable
// for minimal container images.
//
// Despite the issue title ("BSS padding"), the data is intentionally placed
// in `.data` (not `.bss`): uninitialized BSS does not occupy disk bytes, and
// the goal here is on-disk file size parity, not address-space parity.

package main

/*
#include <stddef.h>

// Default to ~14 MiB, the typical size of the real libnvidia-ml.so for
// driver 550.x. Overridable from the Makefile / CGO_CFLAGS.
#ifndef NVML_MOCK_PADDING_BYTES
#define NVML_MOCK_PADDING_BYTES (14 * 1024 * 1024)
#endif

#if NVML_MOCK_PADDING_BYTES > 0

// Non-zero seed byte keeps the array in `.data` (real on-disk bytes) instead
// of being collapsed into `.bss` by smart toolchains. `used` prevents the
// compiler from dropping it, and the dedicated section name makes the blob
// easy to identify with `objdump -h` / `readelf -SW`.
// Place the blob in a named `.data` subsection on Linux (the only platform
// the mock library ships on). The section attribute syntax is platform-
// specific — macOS/Mach-O requires `__SEG,__sect` form — so on non-Linux
// hosts (developer workstations running `go vet` etc.) we fall back to the
// default `.data` section without a custom name.
#if defined(__linux__)
#  define NVML_MOCK_PADDING_ATTRS \
	__attribute__((used, section(".data.nvml_mock_padding"), aligned(64)))
#else
#  define NVML_MOCK_PADDING_ATTRS __attribute__((used, aligned(64)))
#endif

static NVML_MOCK_PADDING_ATTRS
const volatile unsigned char nvml_mock_padding[NVML_MOCK_PADDING_BYTES] = { 0xA5 };

// Tiny accessor anchors the blob across LTO / linker GC by taking its
// address, and lets Go-side tests query the configured padding size without
// re-declaring it.
size_t nvmlMockPaddingSize(void) {
	(void)nvml_mock_padding;
	return (size_t)NVML_MOCK_PADDING_BYTES;
}

#else

size_t nvmlMockPaddingSize(void) { return 0; }

#endif
*/
import "C"

// PaddingBytes reports the compile-time padding size baked into the library.
// Exposed for integration tests / debugging so callers can sanity-check that
// the mock library was built with the expected size profile.
func PaddingBytes() uint64 {
	return uint64(C.nvmlMockPaddingSize())
}
