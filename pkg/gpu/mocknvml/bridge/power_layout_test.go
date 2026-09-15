// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Layout test for nvmlPowerValue_v2_t. See fabric_layout_test.go for why the
// expected size is a constant rather than a C.sizeof_* read.

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// version(4) + powerScope(1) + 3 pad + powerValueMw(4) = 12. The padding is
// the point: powerValueMw is the field the bridge reads, and widening
// powerScope would slide it to offset 4 and read the caller's scope as the
// requested cap.
const expectedPowerValueV2Size uintptr = 12

func TestPowerValueV2Layout(t *testing.T) {
	t.Parallel()
	require.Equal(t, expectedPowerValueV2Size, unsafe.Sizeof(nvml.PowerValue_v2{}),
		"go-nvml's PowerValue_v2 no longer matches the nvml_types.h layout — ABI drift")
}

// TestPowerValueV2Version_MatchesGoNvml pins the tag the bridge accepts against
// the one a caller stamping with go-nvml's STRUCT_VERSION produces. nvidia-smi
// fills this field in before calling; if the two disagree, every `nvidia-smi
// -pl` answers INVALID_ARGUMENT.
func TestPowerValueV2Version_MatchesGoNvml(t *testing.T) {
	t.Parallel()
	require.Equal(t, nvml.STRUCT_VERSION(nvml.PowerValue_v2{}, 2), powerValueV2Version(),
		"bridge demands a different tag than a go-nvml caller stamps; reconcile nvml_types.h")
}
