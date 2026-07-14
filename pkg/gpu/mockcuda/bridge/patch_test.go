//go:build linux && (amd64 || arm64)

package main

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// dataProbe lives in a read/write (non-executable) data segment; its address is
// stable, so mockRangeExecutable must classify it as non-executable.
var dataProbe byte

// TestMockBuildTrampEncoding pins the absolute-jump trampoline byte encoding for
// each supported architecture. A silent change here would redirect the sample's
// runtime calls to the wrong address.
func TestMockBuildTrampEncoding(t *testing.T) {
	const target = 0x1122334455667788
	got := mockBuildTrampBytes(target)

	switch runtime.GOARCH {
	case "amd64":
		// movabs $target,%rax ; jmp *%rax
		want := []byte{
			0x48, 0xB8,
			0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11,
			0xFF, 0xE0,
		}
		require.Equal(t, want, got[:len(want)])
	case "arm64":
		// ldr x16,#8 ; br x16 ; .quad target
		want := []byte{
			0x50, 0x00, 0x00, 0x58,
			0x00, 0x02, 0x1F, 0xD6,
			0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11,
		}
		require.Equal(t, want, got[:len(want)])
	default:
		t.Skipf("no trampoline encoding for GOARCH %s", runtime.GOARCH)
	}
}

// TestMockRangeExecutable covers the patch-eligibility bounds check: a code
// address is accepted, a data address (mapped but not executable) is rejected,
// and a zero-length range is rejected.
func TestMockRangeExecutable(t *testing.T) {
	require.True(t, mockRangeExecutable(mockSelfCodeAddr(), 4),
		"a function address must resolve to an executable mapping")

	dataAddr := uint64(uintptr(unsafe.Pointer(&dataProbe)))
	require.False(t, mockRangeExecutable(dataAddr, 1),
		"a data address must not be classified as executable")

	require.False(t, mockRangeExecutable(mockSelfCodeAddr(), 0),
		"a zero-length range must be rejected")
}
