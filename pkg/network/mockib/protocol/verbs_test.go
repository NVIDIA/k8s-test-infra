// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package protocol

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerbsOp_EncodeDecode_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	op := VerbsOp{
		OpID:       42,
		SrcQPN:     0x101,
		DstQPN:     0x202,
		Opcode:     VerbsOpWrite,
		RemoteAddr: 0xdeadbeef,
		RKey:       0x1234,
		Length:     8,
		Offset:     0,
		More:       false,
		Status:     VerbsStatusSuccess,
		Data:       []byte{1, 2, 3, 4, 5, 6, 7, 8},
	}
	err := WriteMessage(&buf, TypeVerbsOp, op)
	require.NoError(t, err)

	var env Envelope
	require.NoError(t, ReadEnvelope(&buf, &env))
	require.Equal(t, TypeVerbsOp, env.Type)
	var got VerbsOp
	require.NoError(t, DecodeBody(env, &got))
	require.Equal(t, op, got, "verbs_op round-trip")
}

func TestVerbsControl_EncodeDecode(t *testing.T) {
	cases := []struct {
		msgType string
		body    any
	}{
		{TypeVerbsQPCreate, VerbsQPCreateReq{QPN: 0x100, CAName: "mlx5_0", Port: 1}},
		{TypeVerbsQPConnect, VerbsQPConnectReq{LocalQPN: 0x100, DestQPN: 0x200, DLID: 0x0102, DGID: "fe80::1", LinkLayer: "InfiniBand"}},
		{TypeVerbsQPDestroy, VerbsQPDestroyReq{QPN: 0x100}},
		{TypeVerbsAttach, VerbsAttachReq{QPN: 0x100}},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		require.NoError(t, WriteMessage(&buf, tc.msgType, tc.body))
		var env Envelope
		require.NoError(t, ReadEnvelope(&buf, &env))
		require.Equal(t, tc.msgType, env.Type)
	}
}

func TestVerbsOp_PayloadAtMaxFrameSizeRejected(t *testing.T) {
	// A single op whose base64 payload would exceed MaxFrameSize must be
	// rejected by WriteFrame, proving the cap is enforced (the shim chunks to
	// avoid this; see ChunkVerbsOp).
	var buf bytes.Buffer
	op := VerbsOp{
		Opcode: VerbsOpWrite,
		Length: MaxFrameSize,
		Data:   make([]byte, MaxFrameSize), // base64 -> ~4/3 MiB > MaxFrameSize
	}
	err := WriteMessage(&buf, TypeVerbsOp, op)
	require.Error(t, err, "oversize verbs_op should be rejected by frame cap")
}

func TestChunkVerbsOp_Boundaries(t *testing.T) {
	const maxChunk = 1024
	base := VerbsOp{SrcQPN: 1, DstQPN: 2, Opcode: VerbsOpWrite, RemoteAddr: 0x1000, RKey: 7}
	for _, n := range []int{0, 1, maxChunk - 1, maxChunk, maxChunk + 1, 3*maxChunk - 1, 3 * maxChunk} {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(i)
		}
		chunks := ChunkVerbsOp(base, payload, maxChunk)
		require.NotEmpty(t, chunks, "n=%d: expected at least one chunk", n)

		// Every chunk must declare the same total Length and stay under the cap.
		var assembled []byte
		var r Reassembler
		var complete bool
		for i, c := range chunks {
			require.Equal(t, uint32(n), c.Length, "n=%d chunk=%d total length", n, i)
			require.LessOrEqual(t, len(c.Data), maxChunk, "n=%d chunk=%d size", n, i)
			require.Equal(t, base.RemoteAddr, c.RemoteAddr, "n=%d chunk=%d base copied", n, i)
			var got bool
			var data []byte
			var err error
			got, data, err = r.Add(c)
			require.NoError(t, err, "n=%d chunk=%d reassemble", n, i)
			if got {
				complete = true
				assembled = data
			}
		}
		require.True(t, complete, "n=%d: reassembly never completed", n)
		// bytes.Equal treats nil and empty alike, which is what we want for n=0.
		require.True(t, bytes.Equal(payload, assembled), "n=%d: reassembled payload mismatch", n)
	}
}

func TestReassembler_RejectsOutOfOrder(t *testing.T) {
	var r Reassembler
	_, _, err := r.Add(VerbsOp{Length: 10, Offset: 5, More: true, Data: []byte{1, 2, 3}})
	require.Error(t, err, "non-zero starting offset should be rejected")
}

func TestReassembler_RejectsOverLength(t *testing.T) {
	var r Reassembler
	_, _, err := r.Add(VerbsOp{Length: 2, Offset: 0, More: false, Data: []byte{1, 2, 3, 4}})
	require.Error(t, err, "chunk exceeding declared length should be rejected")
}

func TestReassembler_RejectsShortPayload(t *testing.T) {
	var r Reassembler
	_, _, err := r.Add(VerbsOp{Length: 10, Offset: 0, More: false, Data: []byte{1, 2, 3}})
	require.Error(t, err, "final chunk shorter than declared length should be rejected")
}
