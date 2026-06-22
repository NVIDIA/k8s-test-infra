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

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusBWFactor(t *testing.T) {
	cases := []struct {
		name string
		op   Collective
		n    int
		want float64
	}{
		{"allreduce n=2", AllReduce, 2, 1.0},
		{"allreduce n=8", AllReduce, 8, 1.75},
		{"allgather n=8", AllGather, 8, 0.875},
		{"reducescatter n=8", ReduceScatter, 8, 0.875},
		{"broadcast n=8", Broadcast, 8, 1.0},
		{"reduce n=8", Reduce, 8, 1.0},
		{"allreduce n=1", AllReduce, 1, 0.0},
		{"allreduce n=0", AllReduce, 0, 0.0},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			require.InDelta(t, c.want, BusBWFactor(c.op, c.n), 1e-9)
		})
	}
}

func TestParseCollective(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   Collective
		wantOK bool
	}{
		{"all_reduce", "all_reduce", AllReduce, true},
		{"allreduce", "allreduce", AllReduce, true},
		{"all_gather", "all_gather", AllGather, true},
		{"allgather", "allgather", AllGather, true},
		{"reduce_scatter", "reduce_scatter", ReduceScatter, true},
		{"reducescatter", "reducescatter", ReduceScatter, true},
		{"broadcast", "broadcast", Broadcast, true},
		{"bcast", "bcast", Broadcast, true},
		{"reduce", "reduce", Reduce, true},
		{"mixed case", "AllReduce", AllReduce, true},
		{"whitespace padded", "  all_reduce  ", AllReduce, true},
		{"empty string", "", 0, false},
		{"unknown", "nonsense", 0, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseCollective(c.in)
			require.Equal(t, c.wantOK, ok)
			if c.wantOK {
				require.Equal(t, c.want, got)
			}
		})
	}
}
