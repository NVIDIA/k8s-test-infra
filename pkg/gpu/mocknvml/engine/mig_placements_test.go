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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDerivePlacements is written against go-nvml's A100 40GB table, the one
// shipped table that sizes placements in memory units rather than compute
// slices; every row in the a100 block is that table's actual content. The
// rows after it cover a boundary the A100's numbers never reach, a narrower
// board, and the inputs the function refuses.
//
// The 1g.10gb and 2g.10gb rows are the pair that proves the placement count
// needs capping from both directions: identical memory, different compute
// width, four placements against three. The 3g/4g pair makes the same point
// with the caps reversed.
func TestDerivePlacements(t *testing.T) {
	t.Parallel()

	const (
		a100CapacityMB = 40960 // 40 GiB
		h100CapacityMB = 81920 // 80 GiB
		a30CapacityMB  = 24576 // 24 GiB
	)

	tests := []struct {
		name        string
		slices      int
		boardSlices int
		memoryMB    uint64
		capacityMB  uint64
		want        []string
	}{
		{name: "a100 1g.5gb", slices: 1, boardSlices: 7, memoryMB: 4864, capacityMB: a100CapacityMB, want: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		{name: "a100 1g.10gb", slices: 1, boardSlices: 7, memoryMB: 9856, capacityMB: a100CapacityMB, want: []string{"0:2", "2:2", "4:2", "6:2"}},
		{name: "a100 2g.10gb", slices: 2, boardSlices: 7, memoryMB: 9856, capacityMB: a100CapacityMB, want: []string{"0:2", "2:2", "4:2"}},
		{name: "a100 3g.20gb", slices: 3, boardSlices: 7, memoryMB: 19968, capacityMB: a100CapacityMB, want: []string{"0:4", "4:4"}},
		{name: "a100 4g.20gb", slices: 4, boardSlices: 7, memoryMB: 19968, capacityMB: a100CapacityMB, want: []string{"0:4"}},
		{name: "a100 7g.40gb", slices: 7, boardSlices: 7, memoryMB: 40192, capacityMB: a100CapacityMB, want: []string{"0:8"}},

		// An exact eighth must not round up to two units. The A100 rows all
		// sit just under their fraction because real boards reserve memory,
		// so they never exercise the boundary; the H100's do.
		{name: "h100 1g.10gb, an exact eighth", slices: 1, boardSlices: 7, memoryMB: 10240, capacityMB: h100CapacityMB, want: []string{"0:1", "1:1", "2:1", "3:1", "4:1", "5:1", "6:1"}},
		{name: "h100 7g.80gb, the whole board", slices: 7, boardSlices: 7, memoryMB: 81920, capacityMB: h100CapacityMB, want: []string{"0:8"}},

		// A 4-slice board has four memory units, not eight, so the same
		// fractions give different sizes.
		{name: "a30 1g.6gb", slices: 1, boardSlices: 4, memoryMB: 6144, capacityMB: a30CapacityMB, want: []string{"0:1", "1:1", "2:1", "3:1"}},
		{name: "a30 2g.12gb", slices: 2, boardSlices: 4, memoryMB: 12288, capacityMB: a30CapacityMB, want: []string{"0:2", "2:2"}},
		{name: "a30 4g.24gb", slices: 4, boardSlices: 4, memoryMB: 24576, capacityMB: a30CapacityMB, want: []string{"0:4"}},

		// None of these is a board-and-profile pair that exists. They are the
		// shapes a hand-written profile document can take once it feeds this
		// function, and each must be refused rather than answered with a
		// guess: a partition that outgrows its board in either dimension, and
		// the missing or impossible numbers a typo produces.
		{name: "a profile wider than the board has no placement", slices: 7, boardSlices: 4, memoryMB: 24576, capacityMB: a30CapacityMB, want: []string{}},
		{name: "a profile claiming more memory than the board has", slices: 1, boardSlices: 7, memoryMB: 999999, capacityMB: a100CapacityMB, want: []string{}},
		{name: "a board with no capacity has no placement", slices: 1, boardSlices: 7, memoryMB: 10240, capacityMB: 0, want: []string{}},
		{name: "a profile with no memory has no placement", slices: 1, boardSlices: 7, memoryMB: 0, capacityMB: a100CapacityMB, want: []string{}},
		{name: "a profile with no slices has no placement", slices: 0, boardSlices: 7, memoryMB: 4864, capacityMB: a100CapacityMB, want: []string{}},
		{name: "a board wider than NVML's widest profile has no placement", slices: 1, boardSlices: maxGPUInstanceSlices + 1, memoryMB: 4864, capacityMB: a100CapacityMB, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			placements := derivePlacements(tt.slices, tt.boardSlices, tt.memoryMB, tt.capacityMB)
			// A refused profile must yield an empty list, not nil: callers
			// range over and serialise this, and null is not an empty list.
			require.NotNil(t, placements)

			got := []string{}
			for _, p := range placements {
				got = append(got, fmt.Sprintf("%d:%d", p.Start, p.Size))
			}
			require.Equal(t, tt.want, got)
		})
	}
}
