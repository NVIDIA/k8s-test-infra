// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

// major and minor decode a Linux dev_t the way glibc encodes it
// (MMMM Mmmm mmmM MMmm): the major occupies bits 8-19 and 44-63, the minor
// bits 0-7 and 20-43.
//
// These deliberately do not call unix.Major and unix.Minor. Those resolve per
// GOOS: on darwin they decode Darwin's dev_t, where the major is bits 24-31.
// stat.Rdev here always describes a device node on a Linux node, whatever host
// the binary was built on, so the decoding must not follow the build platform.
// devnum_oracle_test.go pins these against unix.Major and unix.Minor so they
// cannot drift from glibc.
func major(dev uint64) uint64 {
	return ((dev & 0x00000000000fff00) >> 8) | ((dev & 0xfffff00000000000) >> 32)
}

func minor(dev uint64) uint64 {
	return (dev & 0x00000000000000ff) | ((dev & 0x00000ffffff00000) >> 12)
}
