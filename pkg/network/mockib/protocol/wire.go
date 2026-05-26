// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Message type strings on the wire.
const (
	TypeRegister = "register"
	TypePing     = "ping"
	TypePong     = "pong"
	TypeOpen     = "open"
	TypeSend     = "send"
	TypeRecv     = "recv"
	TypeClose          = "close"
	TypeRegisterPeers  = "register_peers"
)

// MaxFrameSize is the largest accepted length-prefixed JSON frame.
const MaxFrameSize = 1 << 20 // 1 MiB

// Envelope wraps a typed JSON body for mock-ib RPC and fabric messages.
type Envelope struct {
	Type string          `json:"type"`
	Body json.RawMessage `json:"body"`
}

// PortAdvert describes one HCA port from sysfs or a REGISTER payload.
type PortAdvert struct {
	PortGUID   string `json:"port_guid"`
	DefaultGID string `json:"default_gid,omitempty"`
	CAName     string `json:"ca_name"`
	Port       int    `json:"port"`
	LID        uint16 `json:"lid"`
}

// RegisterBody is the fabric REGISTER message payload.
type RegisterBody struct {
	NodeName string       `json:"node_name"`
	PodIP    string       `json:"pod_ip"`
	Ports    []PortAdvert `json:"ports"`
}

// PingBody is the fabric PING message payload.
type PingBody struct {
	DstPortGUID string `json:"dst_port_guid"`
	DstLID      uint16 `json:"dst_lid,omitempty"`
	Seq         uint32 `json:"seq"`
	ClientTS    int64  `json:"client_ts"`
}

// PongBody is the fabric PONG message payload.
type PongBody struct {
	Seq      uint32 `json:"seq"`
	ServerTS int64  `json:"server_ts"`
}

// WriteEnvelope marshals env and writes a big-endian uint32 length prefix.
func WriteEnvelope(w io.Writer, env Envelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return WriteFrame(w, payload)
}

// ReadEnvelope reads one length-prefixed frame and unmarshals it into env.
func ReadEnvelope(r io.Reader, env *Envelope) error {
	payload, err := ReadFrame(r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	return nil
}

// WriteMessage builds an envelope for msgType and body, then writes it.
func WriteMessage(w io.Writer, msgType string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	return WriteEnvelope(w, Envelope{Type: msgType, Body: raw})
}

// DecodeBody unmarshals env.Body into v.
func DecodeBody(env Envelope, v any) error {
	if err := json.Unmarshal(env.Body, v); err != nil {
		return fmt.Errorf("unmarshal %s body: %w", env.Type, err)
	}
	return nil
}

// WriteFrame writes payload with a 4-byte big-endian length prefix.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("frame too large: %d bytes (max %d)", len(payload), MaxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads one length-prefixed frame from r.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	if n > MaxFrameSize {
		return nil, fmt.Errorf("frame too large: %d bytes (max %d)", n, MaxFrameSize)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
