// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
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
	if err := WriteMessage(&buf, TypeRegister, body); err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := ReadEnvelope(&buf, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != TypeRegister {
		t.Fatalf("type: got %q want %q", env.Type, TypeRegister)
	}
	var got RegisterBody
	if err := DecodeBody(env, &got); err != nil {
		t.Fatal(err)
	}
	if got.NodeName != body.NodeName || got.PodIP != body.PodIP || len(got.Ports) != 1 {
		t.Fatalf("register body mismatch: %+v", got)
	}
	if got.Ports[0].PortGUID != body.Ports[0].PortGUID {
		t.Fatalf("port_guid: got %q want %q", got.Ports[0].PortGUID, body.Ports[0].PortGUID)
	}
}

func TestWriteReadEnvelope_PingPong(t *testing.T) {
	var buf bytes.Buffer
	ping := PingBody{DstPortGUID: "a088:c203:00ab:0001", Seq: 7, ClientTS: 100}
	if err := WriteMessage(&buf, TypePing, ping); err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := ReadEnvelope(&buf, &env); err != nil {
		t.Fatal(err)
	}
	var got PingBody
	if err := DecodeBody(env, &got); err != nil {
		t.Fatal(err)
	}
	if got != ping {
		t.Fatalf("ping: got %+v want %+v", got, ping)
	}

	buf.Reset()
	pong := PongBody{Seq: 7, ServerTS: 200}
	if err := WriteMessage(&buf, TypePong, pong); err != nil {
		t.Fatal(err)
	}
	if err := ReadEnvelope(&buf, &env); err != nil {
		t.Fatal(err)
	}
	var gotPong PongBody
	if err := DecodeBody(env, &gotPong); err != nil {
		t.Fatal(err)
	}
	if gotPong != pong {
		t.Fatalf("pong: got %+v want %+v", gotPong, pong)
	}
}

func TestReadFrame_RejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrameSize+1)
	buf.Write(hdr[:])
	_, err := ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error for oversize frame")
	}
}
