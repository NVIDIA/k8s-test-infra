// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// The drain bridge reinterprets the caller's nvmlPciInfo_t as go-nvml's
// PciInfo and writes an ExcludedDeviceInfo straight into the caller's buffer,
// so the two sides must agree byte for byte. busIdLegacy[16] + five uint32 +
// busId[32] = 68; an excluded entry appends uuid[80] for 148.
func TestDrainLayouts_MatchGoNvml(t *testing.T) {
	t.Parallel()
	require.Equal(t, uintptr(68), pciInfoSizeForTest())
	require.Equal(t, unsafe.Sizeof(nvml.PciInfo{}), pciInfoSizeForTest())

	var info nvml.ExcludedDeviceInfo
	require.Equal(t, uintptr(148), excludedDeviceInfoSizeForTest())
	require.Equal(t, unsafe.Sizeof(info), excludedDeviceInfoSizeForTest())
	require.Equal(t, unsafe.Offsetof(info.Uuid), excludedDeviceInfoUUIDOffsetForTest())
}
