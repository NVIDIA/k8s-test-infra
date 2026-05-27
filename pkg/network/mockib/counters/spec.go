// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package counters owns the deterministic mock-IB counter catalog and
// value Generator. It is consumed by render (initial sysfs file contents)
// and by the daemon ticked writer + PMA synthesizer.
package counters

// Surface is the on-host sysfs directory a counter lives in.
type Surface int

const (
	SurfaceCounters    Surface = iota // sys/class/infiniband/<ca>/ports/1/counters/
	SurfaceHWCounters                 // sys/class/infiniband/<ca>/ports/1/hw_counters/
)

// Family selects which value formula the Generator uses for an Entry.
// See generator.go for the per-family rate model.
type Family int

const (
	FamilyXmitData    Family = iota // bytes/words sent — grows at rate_gbps
	FamilyRcvData                   // bytes/words received — slightly below xmit
	FamilyXmitPackets               // grows at xmit_data / MTU
	FamilyRcvPackets                // grows at rcv_data / MTU
	FamilyError                     // very low rate
	FamilyDiscard                   // very low rate
	FamilyCNP                       // moderate RoCE congestion notification rate
	FamilyAtomic                    // very low atomic-op rate
	FamilyConstant                  // never grows (e.g. lifespan, multicast_*)
)

// Entry binds a counter file name to its on-host surface, on-host bit
// width (controls truncation in the writer + PMA) and value family.
type Entry struct {
	Name    string
	Surface Surface
	Width   int
	Family  Family
}

// Catalog is the single source of truth for which counters exist and
// where. The renderer iterates it to lay down initial files, the daemon
// writer iterates it to rewrite them, and pma.go indexes into it by
// name. Keep entries deterministically ordered (slice, not map) so file
// creation order is stable across runs.
var Catalog = []Entry{
	// counters/ legacy (rendered today as zero)
	{"port_xmit_data", SurfaceCounters, 32, FamilyXmitData},
	{"port_rcv_data", SurfaceCounters, 32, FamilyRcvData},
	{"port_xmit_packets", SurfaceCounters, 32, FamilyXmitPackets},
	{"port_rcv_packets", SurfaceCounters, 32, FamilyRcvPackets},
	{"port_xmit_discards", SurfaceCounters, 32, FamilyDiscard},
	{"port_rcv_errors", SurfaceCounters, 32, FamilyError},
	{"symbol_error", SurfaceCounters, 32, FamilyError},
	{"link_error_recovery", SurfaceCounters, 32, FamilyError},
	{"link_downed", SurfaceCounters, 32, FamilyError},
	{"port_rcv_remote_physical_errors", SurfaceCounters, 32, FamilyError},
	{"port_rcv_switch_relay_errors", SurfaceCounters, 32, FamilyError},
	{"local_link_integrity_errors", SurfaceCounters, 32, FamilyError},
	{"excessive_buffer_overrun_errors", SurfaceCounters, 32, FamilyError},
	{"VL15_dropped", SurfaceCounters, 32, FamilyConstant},
	{"port_xmit_constraint_errors", SurfaceCounters, 32, FamilyError},
	{"port_rcv_constraint_errors", SurfaceCounters, 32, FamilyError},

	// counters/ 64-bit extended
	{"port_xmit_data_64", SurfaceCounters, 64, FamilyXmitData},
	{"port_rcv_data_64", SurfaceCounters, 64, FamilyRcvData},
	{"port_xmit_packets_64", SurfaceCounters, 64, FamilyXmitPackets},
	{"port_rcv_packets_64", SurfaceCounters, 64, FamilyRcvPackets},
	{"unicast_xmit_packets", SurfaceCounters, 64, FamilyXmitPackets},
	{"unicast_rcv_packets", SurfaceCounters, 64, FamilyRcvPackets},
	{"multicast_xmit_packets", SurfaceCounters, 64, FamilyConstant},
	{"multicast_rcv_packets", SurfaceCounters, 64, FamilyConstant},

	// hw_counters/ (mlx5-specific)
	{"out_of_buffer", SurfaceHWCounters, 64, FamilyDiscard},
	{"out_of_sequence", SurfaceHWCounters, 64, FamilyError},
	{"duplicate_request", SurfaceHWCounters, 64, FamilyError},
	{"implied_nak_seq_err", SurfaceHWCounters, 64, FamilyError},
	{"local_ack_timeout_err", SurfaceHWCounters, 64, FamilyError},
	{"packet_seq_err", SurfaceHWCounters, 64, FamilyError},
	{"rnr_nak_retry_err", SurfaceHWCounters, 64, FamilyError},
	{"req_cqe_error", SurfaceHWCounters, 64, FamilyError},
	{"req_cqe_flush_error", SurfaceHWCounters, 64, FamilyError},
	{"req_remote_access_errors", SurfaceHWCounters, 64, FamilyError},
	{"req_remote_invalid_request", SurfaceHWCounters, 64, FamilyError},
	{"resp_cqe_error", SurfaceHWCounters, 64, FamilyError},
	{"resp_cqe_flush_error", SurfaceHWCounters, 64, FamilyError},
	{"resp_local_length_error", SurfaceHWCounters, 64, FamilyError},
	{"resp_remote_access_errors", SurfaceHWCounters, 64, FamilyError},
	{"rx_atomic_requests", SurfaceHWCounters, 64, FamilyAtomic},
	{"rx_read_requests", SurfaceHWCounters, 64, FamilyRcvPackets},
	{"rx_write_requests", SurfaceHWCounters, 64, FamilyRcvPackets},
	{"np_cnp_sent", SurfaceHWCounters, 64, FamilyCNP},
	{"np_ecn_marked_roce_packets", SurfaceHWCounters, 64, FamilyCNP},
	{"rp_cnp_handled", SurfaceHWCounters, 64, FamilyCNP},
	{"rp_cnp_ignored", SurfaceHWCounters, 64, FamilyCNP},
	{"lifespan", SurfaceHWCounters, 64, FamilyConstant},
}

// FindByName returns a pointer into Catalog for the named counter, or
// nil if absent. Used by pma.go to read specific entries by name.
func FindByName(name string) *Entry {
	for i := range Catalog {
		if Catalog[i].Name == name {
			return &Catalog[i]
		}
	}
	return nil
}
