// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package madtest provides shared MAD/UMAD fixtures for mock-ib daemon tests.
package madtest

import (
	"encoding/binary"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/gid"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
)

const (
	MADOffset      = 56
	LIDOffset      = 28
	GRHPresentOff  = 32
	GIDOffset      = 36
	MADClassOff    = 1
	MADMethodOff   = 3
	VendorClass0x81 = 0x81
)

// PingMAD builds a vendor ping-like UMAD buffer targeting port advert p.
func PingMAD(p protocol.PortAdvert) []byte {
	mad := make([]byte, 72)
	binary.LittleEndian.PutUint32(mad[GRHPresentOff:], 1)
	binary.BigEndian.PutUint16(mad[LIDOffset:], p.LID)
	if p.DefaultGID != "" {
		gid.ParseInto(mad[GIDOffset:GIDOffset+16], p.DefaultGID)
	}
	mad[MADOffset+MADClassOff] = VendorClass0x81
	mad[MADOffset+MADMethodOff] = 0x01
	return mad
}

// SAPathQueryMAD builds a minimal SA GET PathRecord UMAD for destination dgid.
func SAPathQueryMAD(dgid []byte) []byte {
	mad := make([]byte, MADOffset+256)
	binary.BigEndian.PutUint16(mad[LIDOffset:], 0x0001)
	m := mad[MADOffset:]
	m[20] = 0x01
	binary.BigEndian.PutUint16(m[24:26], 0x0035)
	copy(m[56:72], dgid)
	return mad
}
