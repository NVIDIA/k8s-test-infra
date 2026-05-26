// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
)

const (
	// Must match libibumad umad_get_mad() (legacy API: &addr.pkey_index), not sizeof(ib_user_mad).
	umadMADOffset  = 56
	umadLIDOffset  = 28
	umadGRHPresent = 32
	umadGIDOffset  = 36
	ibMADClassOff  = 1
	ibMADMethodOff = 3
	ibMADStatusOff = 4
	vendorClass0x81 = 0x81
)

func destLID(umad []byte) (uint16, bool) {
	if len(umad) < umadLIDOffset+2 {
		return 0, false
	}
	// ib_user_mad.addr.lid (uint16) at byte offset 28; libibumad uses network byte order.
	return binary.BigEndian.Uint16(umad[umadLIDOffset:]), true
}

func destGID(umad []byte) (string, bool) {
	if len(umad) < umadGIDOffset+16 {
		return "", false
	}
	if binary.LittleEndian.Uint32(umad[umadGRHPresent:]) == 0 {
		return "", false
	}
	return gid.Format(umad[umadGIDOffset : umadGIDOffset+16]), true
}

func destPortGUID(umad []byte) (string, bool) {
	if len(umad) < umadGIDOffset+16 {
		return "", false
	}
	if binary.LittleEndian.Uint32(umad[umadGRHPresent:]) == 0 {
		return "", false
	}
	g := gid.PortGUIDFromBytes(umad[umadGIDOffset : umadGIDOffset+16])
	if g == "" {
		return "", false
	}
	return g, true
}
