// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package engine implements the mock NCCL cost model, comm/rank state, and
// MPI-free rendezvous backing libnccl.so.2.
package engine

import "strings"

// Collective enumerates the supported collective operations.
type Collective int

const (
	AllReduce Collective = iota
	AllGather
	ReduceScatter
	Broadcast
	Reduce
)

// ParseCollective maps an nccl-tests-style name to a Collective.
func ParseCollective(s string) (Collective, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all_reduce", "allreduce":
		return AllReduce, true
	case "all_gather", "allgather":
		return AllGather, true
	case "reduce_scatter", "reducescatter":
		return ReduceScatter, true
	case "broadcast", "bcast":
		return Broadcast, true
	case "reduce":
		return Reduce, true
	default:
		return 0, false
	}
}

// BusBWFactor returns the nccl-tests bus-bandwidth factor for op across n
// ranks. AllReduce moves 2*(n-1)/n of the buffer; AllGather/ReduceScatter
// move (n-1)/n; Broadcast/Reduce move the full buffer once. n<=1 yields 0
// (no inter-rank traffic), matching real single-rank runs.
func BusBWFactor(op Collective, n int) float64 {
	if n <= 1 {
		return 0
	}
	switch op {
	case AllReduce:
		return 2 * float64(n-1) / float64(n)
	case AllGather, ReduceScatter:
		return float64(n-1) / float64(n)
	default: // Broadcast, Reduce
		return 1
	}
}
