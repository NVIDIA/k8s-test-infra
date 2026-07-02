// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package protocol

import "fmt"

// RDMA verbs data-path message types (issue #374). These ride the same
// length-prefixed JSON transport as the UMAD/ping messages: shim -> local
// daemon over the Unix socket, and daemon -> peer daemon over the TCP fabric.
//
// The data path is a functional artifact relayed over JSON/TCP, NOT an
// InfiniBand measurement; see pkg/network/mockib/README.md.
const (
	// TypeVerbsQPCreate announces a new software QP (shim -> local daemon).
	TypeVerbsQPCreate = "verbs_qp_create"
	// TypeVerbsQPConnect carries the remote endpoint learned at modify_qp(RTR)
	// so the daemon can resolve a peer route (shim -> local daemon).
	TypeVerbsQPConnect = "verbs_qp_connect"
	// TypeVerbsQPDestroy tears a software QP down (shim -> local daemon).
	TypeVerbsQPDestroy = "verbs_qp_destroy"
	// TypeVerbsAttach opens a long-lived inbound stream for one QPN; the daemon
	// pushes inbound verbs_op frames addressed to that QPN back on this same
	// connection (shim -> local daemon, then daemon -> shim).
	TypeVerbsAttach = "verbs_attach"
	// TypeVerbsOp is a single RDMA data-path operation (or one chunk of one).
	// It travels shim -> local daemon (egress), daemon -> peer daemon (fabric),
	// and peer daemon -> owning shim (ingress).
	TypeVerbsOp = "verbs_op"
)

// VerbsOp opcodes.
const (
	VerbsOpWrite    = "write"     // RDMA WRITE: bytes into remote MR
	VerbsOpReadReq  = "read_req"  // RDMA READ request
	VerbsOpReadResp = "read_resp" // RDMA READ response payload (op-id correlated)
	VerbsOpSend     = "send"      // SEND: consumes a remote posted recv WR
)

// VerbsOp status values (mirrors the ibv_wc_status subset we model).
const (
	VerbsStatusSuccess      = "success"
	VerbsStatusRemAccessErr = "rem_access_err"  // bad rkey / out-of-bounds remote_addr
	VerbsStatusRemInvReq    = "rem_inv_req_err" // unknown dst QPN / malformed op
)

// VerbsSegMax is the maximum raw (pre-base64) payload carried in a single
// verbs_op chunk. JSON base64 inflates by 4/3 and the envelope adds overhead,
// so this stays comfortably under protocol.MaxFrameSize (1 MiB). Larger
// post_send payloads are split into multiple chunks (Offset/More) by the shim;
// MaxFrameSize itself is never raised.
const VerbsSegMax = 512 * 1024

// VerbsQPCreateReq announces a software QP to the local daemon. The QPN is
// assigned by the shim (the daemon never allocates QPNs); CAName/Port identify
// the local mock HCA the QP belongs to for diagnostics.
type VerbsQPCreateReq struct {
	QPN    uint32 `json:"qpn"`
	CAName string `json:"ca_name,omitempty"`
	Port   int    `json:"port,omitempty"`
}

// VerbsQPConnectReq carries the remote endpoint the shim learned from
// modify_qp(->RTR): the peer's QPN plus its LID and/or GID. The daemon resolves
// the destination pod IP from the registry, GID/GUID first then LID.
type VerbsQPConnectReq struct {
	LocalQPN  uint32 `json:"local_qpn"`
	DestQPN   uint32 `json:"dest_qpn"`
	DLID      uint16 `json:"dlid,omitempty"`
	DGID      string `json:"dgid,omitempty"`
	LinkLayer string `json:"link_layer,omitempty"`
}

// VerbsQPDestroyReq tears down a software QP and its route/attach state.
type VerbsQPDestroyReq struct {
	QPN uint32 `json:"qpn"`
}

// VerbsAttachReq registers an inbound stream for QPN on the connection it is
// sent on. The daemon keeps that connection and writes inbound verbs_op frames
// (addressed to QPN) back on it.
type VerbsAttachReq struct {
	QPN uint32 `json:"qpn"`
}

// VerbsOp is one RDMA data-path operation, possibly one chunk of a larger
// payload. Length is the TOTAL operation length; Offset/More describe this
// chunk's slice of it. Data is base64-encoded on the wire.
type VerbsOp struct {
	OpID       uint64 `json:"op_id"`
	SrcQPN     uint32 `json:"src_qpn"`
	DstQPN     uint32 `json:"dst_qpn"`
	Opcode     string `json:"opcode"`
	RemoteAddr uint64 `json:"remote_addr,omitempty"`
	RKey       uint32 `json:"rkey,omitempty"`
	Length     uint32 `json:"length"`
	Offset     uint32 `json:"offset,omitempty"`
	More       bool   `json:"more,omitempty"`
	Status     string `json:"status,omitempty"`
	Data       []byte `json:"data,omitempty"`
}

// ChunkVerbsOp splits payload into one or more VerbsOp chunks under maxChunk
// raw bytes each, copying base into every chunk and setting Length (total),
// Offset, More, and Data. A zero-length payload yields a single empty chunk so
// even bytes-free ops (e.g. a bounds-error read_resp) traverse the fabric.
//
// This mirrors the shim's C chunking; it is also unit-tested for the at /
// just-under / just-over boundary cases that the responder reassembles by
// Offset back into the registered MR.
func ChunkVerbsOp(base VerbsOp, payload []byte, maxChunk int) []VerbsOp {
	if maxChunk <= 0 {
		maxChunk = VerbsSegMax
	}
	total := uint32(len(payload))
	base.Length = total
	if len(payload) == 0 {
		base.Offset = 0
		base.More = false
		base.Data = nil
		return []VerbsOp{base}
	}
	var out []VerbsOp
	for off := 0; off < len(payload); off += maxChunk {
		end := off + maxChunk
		if end > len(payload) {
			end = len(payload)
		}
		chunk := base
		chunk.Length = total
		chunk.Offset = uint32(off)
		chunk.More = end < len(payload)
		chunk.Data = append([]byte(nil), payload[off:end]...)
		out = append(out, chunk)
	}
	return out
}

// Reassembler rebuilds a single chunked VerbsOp payload from its Offset/More
// chunks. It enforces contiguous, in-order, in-bounds chunks so a malformed or
// hostile stream is rejected with an error rather than over-writing a buffer.
type Reassembler struct {
	buf  []byte
	next uint32 // next expected offset
	done bool
}

// Add ingests one chunk. It returns complete=true with the fully assembled
// payload once a chunk with More=false has been received. An out-of-order,
// overlapping, or over-Length chunk is an error.
func (r *Reassembler) Add(op VerbsOp) (complete bool, payload []byte, err error) {
	if r.done {
		return false, nil, fmt.Errorf("reassembler: chunk after completion")
	}
	if op.Offset != r.next {
		return false, nil, fmt.Errorf("reassembler: out-of-order chunk offset=%d want=%d", op.Offset, r.next)
	}
	if uint64(op.Offset)+uint64(len(op.Data)) > uint64(op.Length) {
		return false, nil, fmt.Errorf("reassembler: chunk exceeds declared length (off=%d len=%d total=%d)",
			op.Offset, len(op.Data), op.Length)
	}
	r.buf = append(r.buf, op.Data...)
	r.next += uint32(len(op.Data))
	if op.More {
		return false, nil, nil
	}
	if r.next != op.Length {
		return false, nil, fmt.Errorf("reassembler: short payload (have=%d want=%d)", r.next, op.Length)
	}
	r.done = true
	return true, r.buf, nil
}
