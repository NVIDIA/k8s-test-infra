// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// Captured from `ibping -c 1 3520` (dest LID 0x0dc0) via libibmockumad → mock-ib send RPC.
const ibpingLID3520MAD = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABgAEAAA3AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABMgEBAAAAAAAAAAAVuVeDAAAAAAAAAAAAAAAAAAAAAAAAAAAAABQFAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="

func TestDestLID_RealIbpingSend(t *testing.T) {
	mad, err := base64.StdEncoding.DecodeString(ibpingLID3520MAD)
	require.NoError(t, err)
	lid, ok := destLID(mad)
	require.True(t, ok, "destLID: not found")
	require.Equal(t, uint16(0x0dc0), lid)
}
