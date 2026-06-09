// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"testing"
)

func TestPathRecordAttrOffset(t *testing.T) {
	mad := make([]byte, 128)
	binary.BigEndian.PutUint16(mad[24:26], ibSAAttrPathRecord)
	off, ok := pathRecordAttrOffset(mad)
	if !ok || off != 24 {
		t.Fatalf("got off=%d ok=%v", off, ok)
	}
}

func TestPathRecordDGIDOffset_RMPP(t *testing.T) {
	mad := make([]byte, 128)
	binary.BigEndian.PutUint16(mad[24:26], ibSAAttrPathRecord)
	copy(mad[72:74], []byte{0xfe, 0x80})
	off, ok := pathRecordDGIDOffset(mad)
	if !ok || off != 72 {
		t.Fatalf("got off=%d ok=%v", off, ok)
	}
}

func TestPathRecordDGIDOffset_Fixed(t *testing.T) {
	mad := make([]byte, ibPathRecDGIDOff+16)
	copy(mad[ibPathRecDGIDOff:ibPathRecDGIDOff+2], []byte{0xfe, 0x80})
	off, ok := pathRecordDGIDOffset(mad)
	if !ok || off != ibPathRecDGIDOff {
		t.Fatalf("got off=%d ok=%v", off, ok)
	}
}

func TestSetSAMethodResponse(t *testing.T) {
	tests := []struct {
		name  string
		setup func([]byte)
		check func([]byte) bool
	}{
		{
			// Standard-aligned MAD: method at wire byte 3, AttributeID at 16.
			// The response bit must land on the method byte and BaseVersion
			// (byte 0) must be left intact — the old mad[0] shortcut corrupted it.
			name: "method at byte 3, BaseVersion preserved",
			setup: func(m []byte) {
				m[0] = 0x01 // BaseVersion
				m[ibMADMethodOff] = ibSAMethodGet
				binary.BigEndian.PutUint16(m[ibMADCommonHdrLen:ibMADCommonHdrLen+2], ibSAAttrPathRecord)
			},
			check: func(m []byte) bool { return m[0] == 0x01 && m[ibMADMethodOff] == 0x81 },
		},
		{
			// Layout used by madtest.SAPathQueryMAD: method one word ahead of attr.
			name: "method before attr",
			setup: func(m []byte) {
				binary.BigEndian.PutUint16(m[24:26], ibSAAttrPathRecord)
				m[20] = ibSAMethodGet
			},
			check: func(m []byte) bool { return m[20] == 0x81 },
		},
		{
			name: "TRID bytes untouched",
			setup: func(m []byte) {
				copy(m[8:16], []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})
				binary.BigEndian.PutUint16(m[24:26], ibSAAttrPathRecord)
				m[12] = 0x01 // would match GET if scan did not skip 8-15
				m[20] = ibSAMethodGet
			},
			check: func(m []byte) bool {
				return m[12] == 0x01 && m[12]&0x80 == 0 && m[20] == 0x81
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mad := make([]byte, 128)
			tc.setup(mad)
			setSAMethodResponse(mad)
			if !tc.check(mad) {
				t.Fatalf("check failed for %x", mad[:32])
			}
		})
	}
}

func TestSynthesizeSAPathRecordResp_PreservesTRID(t *testing.T) {
	send := make([]byte, umadMADOffset+256)
	copy(send[umadMADOffset+8:umadMADOffset+16], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	resp := synthesizeSAPathRecordResp(send, 0x0300)
	mad := resp[umadMADOffset:]
	if got, want := mad[8:16], send[umadMADOffset+8:umadMADOffset+16]; string(got) != string(want) {
		t.Fatalf("TRID: got %x want %x", got, want)
	}
}
