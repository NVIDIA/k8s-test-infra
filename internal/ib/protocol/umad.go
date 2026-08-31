// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package protocol

// OpenReq opens a UMAD port on the local mock CA.
type OpenReq struct {
	CAName string `json:"ca_name"`
	Port   int    `json:"port"`
}

// OpenResp returns a handle for subsequent send/recv/close.
type OpenResp struct {
	Handle int    `json:"handle"`
	Error  string `json:"error,omitempty"`
}

// SendReq sends a UMAD buffer (libibumad layout) on handle.
type SendReq struct {
	Handle int    `json:"handle"`
	MAD    []byte `json:"mad"`
}

// SendResp acknowledges a send.
type SendResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// RecvReq waits for the next inbound MAD on handle.
type RecvReq struct {
	Handle    int `json:"handle"`
	TimeoutMS int `json:"timeout_ms"`
}

// RecvResp delivers one MAD or a timeout.
type RecvResp struct {
	MAD     []byte `json:"mad,omitempty"`
	Timeout bool   `json:"timeout,omitempty"`
	Error   string `json:"error,omitempty"`
}

// CloseReq closes a handle.
type CloseReq struct {
	Handle int `json:"handle"`
}

// CloseResp acknowledges close.
type CloseResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}
