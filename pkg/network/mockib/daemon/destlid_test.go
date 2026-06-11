// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/base64"
	"testing"
)

// Captured from `ibping -c 1 3520` (dest LID 0x0dc0) via libibmockumad → mock-ib send RPC.
const ibpingLID3520MAD = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABgAEAAA3AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABMgEBAAAAAAAAAAAVuVeDAAAAAAAAAAAAAAAAAAAAAAAAAAAAABQFAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="

func TestDestLID_RealIbpingSend(t *testing.T) {
	mad, err := base64.StdEncoding.DecodeString(ibpingLID3520MAD)
	if err != nil {
		t.Fatal(err)
	}
	lid, ok := destLID(mad)
	if !ok {
		t.Fatal("destLID: not found")
	}
	if lid != 0x0dc0 {
		t.Fatalf("destLID: got 0x%04x want 0x0dc0", lid)
	}
}
