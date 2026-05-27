// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/counters"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/subnet"
)

// PMA wire constants (IB spec vol 1, ch 16; libibmad mad.h). MgmtClass
// 0x04 is Performance Management; Get/Set carry counter queries from
// perfquery and Prometheus RDMA exporters.
const (
	pmaClass         byte = 0x04
	pmaMethodGet     byte = 0x01
	pmaMethodSet     byte = 0x02
	pmaMethodGetResp byte = 0x81
)

// PMAAttrXxx are exposed so test fixtures and the dispatcher hook (Task
// 3.5) can reference them without rebinding the literals.
const (
	PMAAttrClassPortInfo   uint16 = 0x0001
	PMAAttrPortCounters    uint16 = 0x0012
	PMAAttrPortCountersExt uint16 = 0x001D
)

// pmaUMADMinLen is the smallest umad+mad payload we'll touch. Matches
// the SMP path constant from subnet.synthesize.go.
const pmaUMADMinLen = 56 + 24 // umad header + MAD header

// IsPMASend reports whether umad carries a Performance Management
// Get or Set. The handler hook in server.go uses this to route PMA MADs
// to TrySynthesizePMA before the SMP / fabric / loopback fallbacks.
func IsPMASend(umad []byte) bool {
	if len(umad) < pmaUMADMinLen {
		return false
	}
	mad := umad[56:]
	hdr, ok := normalizeMADHeaderForPMA(mad)
	if !ok {
		return false
	}
	if hdr[1] != pmaClass {
		return false
	}
	if hdr[3]&0x80 != 0 {
		return false
	}
	method := hdr[3] & 0x7f
	return method == pmaMethodGet || method == pmaMethodSet
}

// TrySynthesizePMA returns a synthesized RECV umad for the given PMA
// request, or (nil,false) when the request is unsupported. Subsequent
// tasks (3.2-3.4) populate the AttrID switch; the skeleton stays inert
// so Task 3.5 can wire the dispatcher without behavior changes.
func TrySynthesizePMA(sendMad []byte, localCA string,
	gen counters.Generator, epochs *counters.Epochs, now time.Time) ([]byte, bool) {
	_ = sendMad
	_ = localCA
	_ = gen
	_ = epochs
	_ = now
	return nil, false
}

// normalizeMADHeaderForPMA mirrors subnet.normalizeMADHeader since that
// helper is package-private. Keep the two implementations identical;
// PMA and SMP share the same word-swapped wire layout.
func normalizeMADHeaderForPMA(mad []byte) ([24]byte, bool) {
	// Delegate to subnet via a thin shim: re-use the exported MAD
	// helpers so we don't drift from the SMP decoder.
	if len(mad) < 24 {
		return [24]byte{}, false
	}
	var hdr [24]byte
	for w := 0; w < len(hdr); w += 4 {
		hdr[w+0] = mad[w+3]
		hdr[w+1] = mad[w+2]
		hdr[w+2] = mad[w+1]
		hdr[w+3] = mad[w+0]
	}
	_ = subnet.GetField // silence import while skeleton is unused; remove in Task 3.2.
	return hdr, true
}
