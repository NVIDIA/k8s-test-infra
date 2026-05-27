// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package counters

import (
	"encoding/binary"
	"hash/fnv"
	"time"
)

// Generator produces deterministic counter values keyed by (caIdx, entry,
// elapsed). All values are reproducible: given the same NodeID, RateGbps,
// caIdx, entry, and elapsed duration, Value returns the same uint64.
type Generator struct {
	NodeID   uint16 // FNV-1a(NODE_NAME); 0 for unnamed.
	RateGbps int    // Profile link rate; controls FamilyXmit/Rcv rate.
}

// Value returns the counter value at the given elapsed duration since
// the per-port reset epoch. Width (32 or 64) is enforced via mod 2^Width.
func (g Generator) Value(caIdx int, e Entry, elapsed time.Duration) uint64 {
	seed := g.seed(caIdx, e.Name, e.Family)
	raw := seed + g.delta(e.Family, e.Name, elapsed)
	if e.Width == 32 {
		return raw & 0xffffffff
	}
	return raw
}

// seed is a small deterministic offset so two pods/CAs have visibly
// distinct counter values at elapsed=0 (otherwise an exporter scraping
// immediately after pod start would read identical numbers across nodes).
// Range is bounded per-family to keep the seed dwarfed by traffic growth.
func (g Generator) seed(caIdx int, name string, family Family) uint64 {
	h := fnv.New32a()
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(g.NodeID)<<16|uint32(uint16(caIdx)))
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(name))
	raw := uint64(h.Sum32()) & 0x00ffffff // low 24 bits
	switch family {
	case FamilyError:
		return raw % 5
	case FamilyDiscard:
		return raw % 3
	case FamilyCNP:
		return raw % 1000
	case FamilyAtomic:
		return raw % 100
	case FamilyConstant:
		return raw % 10
	case FamilyXmitData, FamilyRcvData:
		rps := g.bytesPerSec()
		if rps == 0 {
			return raw
		}
		return raw % rps
	case FamilyXmitPackets, FamilyRcvPackets:
		return raw % 10000
	default:
		return raw
	}
}

// delta is the elapsed-time contribution to the counter value, per
// family. Math uses float64 for clarity; values are well within float64
// precision for our scales (RateGbps<=1000, elapsed<=days).
func (g Generator) delta(family Family, name string, elapsed time.Duration) uint64 {
	if elapsed <= 0 {
		return 0
	}
	secs := elapsed.Seconds()
	bps := float64(g.bytesPerSec())
	switch family {
	case FamilyXmitData:
		// 70% utilization; sysfs port_xmit_data is in 4-byte words per IB spec.
		return uint64(bps * secs * 0.7 / 4.0)
	case FamilyRcvData:
		return uint64(bps * secs * 0.65 / 4.0)
	case FamilyXmitPackets:
		return uint64(bps * secs * 0.7 / 4096.0)
	case FamilyRcvPackets:
		return uint64(bps * secs * 0.65 / 4096.0)
	case FamilyError:
		return uint64(secs * 0.05)
	case FamilyDiscard:
		return uint64(secs * 0.02)
	case FamilyCNP:
		return uint64(secs * 5)
	case FamilyAtomic:
		return uint64(secs * 0.2)
	case FamilyConstant:
		return 0
	default:
		return 0
	}
}

// bytesPerSec converts RateGbps to bytes/s. Returns 0 when unset, which
// forces data/packet counters to stay at their seed value.
func (g Generator) bytesPerSec() uint64 {
	if g.RateGbps <= 0 {
		return 0
	}
	return uint64(g.RateGbps) * 1_000_000_000 / 8
}
