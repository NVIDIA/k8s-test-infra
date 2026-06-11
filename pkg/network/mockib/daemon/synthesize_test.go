// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// Real ibping send buffer (see destlid_test.go).
const ibpingSendMAD = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABgAEAAA3AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABMgEBAAAAAAAAAAAVuVeDAAAAAAAAAAAAAAAAAAAAAAAAAAAAABQFAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="

func TestSynthesizeRecv_PreservesTrid(t *testing.T) {
	send, err := base64.StdEncoding.DecodeString(ibpingSendMAD)
	require.NoError(t, err)
	lb := NewLoopback(nil)
	recv := lb.SynthesizeRecv(send)
	require.GreaterOrEqual(t, len(recv), umadMADOffset+8, "short recv buffer")
	sendMAD := send[umadMADOffset : umadMADOffset+16]
	recvMAD := recv[umadMADOffset : umadMADOffset+16]
	require.Equal(t, sendMAD[4:8], recvMAD[4:8], "TRID bytes 4-7 changed")
	require.NotZero(t, recvMAD[ibMADMethodOff]&0x80, "expected response method bit set")
}
