//go:build linux && (amd64 || arm64)

package main

// Thin Go wrappers over the internal C patch helpers in cuda.go. cgo is not
// permitted directly in _test.go files, so these live in a regular source file
// and are exercised by patch_test.go. They add no exported C symbols and are
// inert at runtime (nothing in the mock's normal path calls them).

/*
#include "patch.h"

static void testBuildTramp(unsigned char* code, unsigned long target) {
	mockBuildTramp(code, (void*)target);
}

// testCodeAddr returns the address of a known function so tests can probe
// mockRangeExec with a real code pointer.
static unsigned long testCodeAddr(void) {
	return (unsigned long)(void*)&mockBuildTramp;
}
*/
import "C"

import "unsafe"

// mockBuildTrampBytes returns the absolute-jump trampoline bytes the loader
// would write to redirect an entry point to target.
func mockBuildTrampBytes(target uint64) []byte {
	var code [16]C.uchar
	C.testBuildTramp(&code[0], C.ulong(target))
	return C.GoBytes(unsafe.Pointer(&code[0]), C.int(len(code)))
}

// mockRangeExecutable reports whether [addr, addr+length) lies within an
// executable mapping (the patch-eligibility bounds check).
func mockRangeExecutable(addr uint64, length int) bool {
	return C.mockRangeExec(C.ulong(addr), C.size_t(length)) != 0
}

// mockSelfCodeAddr returns an address known to live in an executable mapping.
func mockSelfCodeAddr() uint64 {
	return uint64(C.testCodeAddr())
}
