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

// Pure-Go helper for classifying caller-supplied fabric struct version
// tags. Lives in a non-cgo file so a Go test (which cannot use cgo in
// packages that contain //export directives) can exercise the dispatch
// decision without going through the .so entry point.

package main

// FabricVersionKind classifies which NVML fabric struct version a
// caller is asking for.
type FabricVersionKind int

const (
	// FabricVersionInvalid means the version tag does not match any
	// supported NVML fabric struct layout. nvmlDeviceGetGpuFabricInfoV
	// must return NVML_ERROR_ARGUMENT_VERSION_MISMATCH in this case,
	// which matches real NVML behaviour.
	FabricVersionInvalid FabricVersionKind = iota
	// FabricVersionV2 selects nvmlGpuFabricInfo_v2_t.
	FabricVersionV2
	// FabricVersionV3 selects nvmlGpuFabricInfo_v3_t (also exposed as
	// nvmlGpuFabricInfoV_t).
	FabricVersionV3
)

// ClassifyFabricVersion returns the layout selected by a caller-supplied
// version tag. v2Tag and v3Tag must be the encoded forms produced by
// FabricStructVersion(size, N) for the corresponding struct. Any other
// value — including zero — is rejected as Invalid.
//
// Real NVML returns NVML_ERROR_ARGUMENT_VERSION_MISMATCH for unknown
// version tags. Accepting zero as a synonym for "latest" was a path
// that could write the v3 layout into a v2-sized buffer when a caller
// forgot to set Version before calling. This helper makes the decision
// explicit and testable.
func ClassifyFabricVersion(requested, v2Tag, v3Tag uint32) FabricVersionKind {
	switch requested {
	case v2Tag:
		return FabricVersionV2
	case v3Tag:
		return FabricVersionV3
	default:
		return FabricVersionInvalid
	}
}
