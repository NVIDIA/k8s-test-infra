// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/base64"
	"testing"
)

// Real ibping send buffer (see destlid_test.go).
const ibpingSendMAD = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABgAEAAA3AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABMgEBAAAAAAAAAAAVuVeDAAAAAAAAAAAAAAAAAAAAAAAAAAAAABQFAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="

func TestSynthesizeRecv_PreservesTrid(t *testing.T) {
	send, err := base64.StdEncoding.DecodeString(ibpingSendMAD)
	if err != nil {
		t.Fatal(err)
	}
	lb := NewLoopback(nil)
	recv := lb.SynthesizeRecv(send)
	if len(recv) < umadMADOffset+8 {
		t.Fatal("short recv buffer")
	}
	sendMAD := send[umadMADOffset : umadMADOffset+16]
	recvMAD := recv[umadMADOffset : umadMADOffset+16]
	if sendMAD[4] != recvMAD[4] || sendMAD[5] != recvMAD[5] || sendMAD[6] != recvMAD[6] || sendMAD[7] != recvMAD[7] {
		t.Fatalf("TRID bytes 4-7 changed: send %x recv %x", sendMAD[4:8], recvMAD[4:8])
	}
	if recvMAD[ibMADMethodOff]&0x80 == 0 {
		t.Fatal("expected response method bit set")
	}
}
