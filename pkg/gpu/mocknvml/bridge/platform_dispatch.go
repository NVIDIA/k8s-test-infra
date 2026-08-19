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

// Pure-Go helper for classifying caller-supplied platform-info struct version
// tags. Lives in a non-cgo file so a Go test (which cannot use cgo in packages
// that contain //export directives) can exercise the dispatch decision without
// going through the .so entry point, following fabric_dispatch.go.

package main

// PlatformVersionKind classifies which nvmlPlatformInfo_t struct version a
// caller is asking for.
type PlatformVersionKind int

const (
	// PlatformVersionInvalid means the version tag matches no supported
	// layout. nvmlDeviceGetPlatformInfo must then return
	// NVML_ERROR_ARGUMENT_VERSION_MISMATCH, as the upstream header documents.
	PlatformVersionInvalid PlatformVersionKind = iota
	// PlatformVersionV1 selects nvmlPlatformInfo_v1_t, deprecated upstream.
	PlatformVersionV1
	// PlatformVersionV2 selects nvmlPlatformInfo_v2_t (also exposed as
	// nvmlPlatformInfo_t).
	PlatformVersionV2
)

// ClassifyPlatformVersion returns the layout selected by a caller-supplied
// version tag. v1Tag and v2Tag must be the encoded forms produced by
// FabricStructVersion(size, N) for the corresponding struct. Any other value —
// including zero — is rejected as Invalid, so a caller that forgot to set
// Version gets an error rather than a payload written to a layout it did not
// ask for.
//
// The two versions describe the same 44 bytes under different field names, so
// unlike the fabric dispatch this classification does not change what the
// bridge writes; it only decides whether to write at all and which tag to echo
// back. It stays strict anyway: a caller passing a garbage tag is confused
// about the ABI, and silently answering it would hide that.
func ClassifyPlatformVersion(requested, v1Tag, v2Tag uint32) PlatformVersionKind {
	switch requested {
	case v1Tag:
		return PlatformVersionV1
	case v2Tag:
		return PlatformVersionV2
	default:
		return PlatformVersionInvalid
	}
}
