// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// nvidia-smi bounds its reads of the table by the size word. A size word
// larger than the table is what made a 615 nvidia-smi call through whatever
// followed it, so the two must agree, and every slot it may read must hold a
// trampoline.
func TestExportTable_IsAsLongAsItsSizeWord(t *testing.T) {
	initExportTable()

	require.Equal(t, uintptr(len(internalExportTable))*unsafe.Sizeof(uintptr(0)), internalExportTable[0])
	for slot := 1; slot < len(internalExportTable); slot++ {
		require.NotZerof(t, internalExportTable[slot], "slot %d has no trampoline", slot)
	}
}

// lowWord and highWord split one 64-bit block word into the two 32-bit fields
// it holds on the little-endian targets the mock builds for.
func lowWord(w uint64) uint32  { return uint32(w) }
func highWord(w uint64) uint32 { return uint32(w >> 32) }

// A 615 nvidia-smi counts unlisted GPUs before every command that changes GPU
// state, and trusts the count on SUCCESS: the slot must overwrite it, and leave
// a block of any other version alone.
func TestUnlistedGPUCountSlot_ReportsNone(t *testing.T) {
	const garbage = uint64(0xffff) << 32

	block, ret := internalSlotBlockCallForTest(unlistedGPUCountSlotForTest,
		[]uint64{garbage | uint64(unlistedGPUCountV1ForTest)}, 0, 0, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(unlistedGPUCountV1ForTest), lowWord(block[0]), "the version tag is left as it was")
	require.Zero(t, highWord(block[0]), "no unlisted GPUs")

	other := garbage | 0x02000010
	block, ret = internalSlotBlockCallForTest(unlistedGPUCountSlotForTest, []uint64{other}, 0, 0, 0)
	require.Equal(t, nvml.ERROR_ARGUMENT_VERSION_MISMATCH, ret)
	require.Equal(t, other, block[0], "a block of another version is not written")
}

// `nvidia-smi --gpu-reset` on a 615 binary allocates a group of the GPUs it
// resets, runs the group and frees it. The run only succeeds on the group the
// allocation handed out, and then reports a reset of the GPUs alone, whatever
// the caller left in the scope word.
func TestResetGroupSlots_RunTheGroupTheyAllocate(t *testing.T) {
	const pciInfoWords = 12 // room for one nvmlPciInfo_t, which the slot only checks for

	_, ret := internalSlotBlockCallForTest(resetGroupAllocSlotForTest, []uint64{0}, pciInfoWords, 0, 1)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "a group of no GPUs")

	out, ret := internalSlotBlockCallForTest(resetGroupAllocSlotForTest, []uint64{0}, pciInfoWords, 1, 1)
	require.Equal(t, nvml.SUCCESS, ret)
	group := out[0]
	require.NotZero(t, group, "the allocation hands out a group")

	const prefill = 0xdeadbeef
	scope, ret := resetGroupRunForTest(resetGroupRunV1ForTest, group, prefill)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, scope, "no NVSwitch was reset")

	scope, ret = resetGroupRunForTest(resetGroupRunV1ForTest, group+1, prefill)
	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "a group it never handed out")
	require.Equal(t, uint32(prefill), scope, "a failed run writes nothing")

	scope, ret = resetGroupRunForTest(0x02000040, group, prefill)
	require.Equal(t, nvml.ERROR_ARGUMENT_VERSION_MISMATCH, ret)
	require.Equal(t, uint32(prefill), scope, "a failed run writes nothing")
}

// Entry points of a newer nvidia-smi that the mock does not implement decline
// rather than report success, because the caller would read the
// out-parameters nothing wrote.
func TestExportTable_UnimplementedNewerSlotsDecline(t *testing.T) {
	for _, slot := range []uint{firstSlotPast580ForTest, 271, 300, uint(len(internalExportTable) - 1)} {
		_, ret := internalSlotBlockCallForTest(slot, []uint64{0}, 0, 0, 0)
		require.Equalf(t, nvml.ERROR_NOT_SUPPORTED, ret, "slot %d", slot)
	}
}
