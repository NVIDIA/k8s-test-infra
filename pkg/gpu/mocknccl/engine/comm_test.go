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
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommRunCollectiveSleepsAndReportsTime(t *testing.T) {
	comm := &Comm{
		Rank:      0,
		WorldSize: 2,
		InterNode: true,
		Model:     Model{LatencyUS: 0, Efficiency: 1, InterBytesPerSec: 1e9}, // 1 GB/s
		MaxSleep:  10 * time.Millisecond,
	}
	// 1 MB at 1 GB/s, factor(AllReduce,2)=1 => ~1ms modeled, under the cap.
	d := comm.RunCollective(AllReduce, 1<<20)
	require.GreaterOrEqual(t, d, 900*time.Microsecond)
	require.LessOrEqual(t, d, 5*time.Millisecond)
}

func TestCommRunCollectiveRespectsCap(t *testing.T) {
	comm := &Comm{
		Rank: 0, WorldSize: 2, InterNode: true,
		Model:    Model{LatencyUS: 0, Efficiency: 1, InterBytesPerSec: 1e9},
		MaxSleep: 2 * time.Millisecond,
	}
	start := time.Now()
	_ = comm.RunCollective(AllReduce, 1<<30) // huge => modeled >> cap
	elapsed := time.Since(start)
	// Huge message => sleep = min(modeled, MaxSleep) = MaxSleep, and
	// time.Sleep guarantees at least that duration (non-flaky lower bound).
	require.GreaterOrEqual(t, elapsed, comm.MaxSleep)
	require.LessOrEqual(t, elapsed, 50*time.Millisecond)
}
