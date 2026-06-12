// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteReadEnvelope_Register(t *testing.T) {
	var buf bytes.Buffer
	body := RegisterBody{
		NodeName: "worker-a",
		PodIP:    "10.0.0.2",
		Ports: []PortAdvert{{
			PortGUID: "a088:c203:00ab:0001",
			CAName:   "mlx5_0",
			Port:     1,
			LID:      0x0100,
		}},
	}
	err := WriteMessage(&buf, TypeRegister, body)
	require.NoError(t, err)
	var env Envelope
	err = ReadEnvelope(&buf, &env)
	require.NoError(t, err)
	require.Equal(t, TypeRegister, env.Type, "type")
	var got RegisterBody
	err = DecodeBody(env, &got)
	require.NoError(t, err)
	require.Equal(t, body.NodeName, got.NodeName, "register body mismatch: %+v", got)
	require.Equal(t, body.PodIP, got.PodIP, "register body mismatch: %+v", got)
	require.Len(t, got.Ports, 1, "register body mismatch: %+v", got)
	require.Equal(t, body.Ports[0].PortGUID, got.Ports[0].PortGUID, "port_guid")
}

func TestWriteReadEnvelope_PingPong(t *testing.T) {
	var buf bytes.Buffer
	ping := PingBody{DstPortGUID: "a088:c203:00ab:0001", Seq: 7, ClientTS: 100}
	err := WriteMessage(&buf, TypePing, ping)
	require.NoError(t, err)
	var env Envelope
	err = ReadEnvelope(&buf, &env)
	require.NoError(t, err)
	var got PingBody
	err = DecodeBody(env, &got)
	require.NoError(t, err)
	require.Equal(t, ping, got, "ping")

	buf.Reset()
	pong := PongBody{Seq: 7, ServerTS: 200}
	err = WriteMessage(&buf, TypePong, pong)
	require.NoError(t, err)
	err = ReadEnvelope(&buf, &env)
	require.NoError(t, err)
	var gotPong PongBody
	err = DecodeBody(env, &gotPong)
	require.NoError(t, err)
	require.Equal(t, pong, gotPong, "pong")
}

func TestReadFrame_RejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrameSize+1)
	buf.Write(hdr[:])
	_, err := ReadFrame(&buf)
	require.Error(t, err, "expected error for oversize frame")
}
