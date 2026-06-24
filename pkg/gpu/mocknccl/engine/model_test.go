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

func TestOpTimeLatencyBound(t *testing.T) {
	// Tiny message: dominated by latency (~10us).
	m := Model{LatencyUS: 10, Efficiency: 1.0, InterBytesPerSec: 50e9}
	got := m.OpTime(AllReduce, 8, 2, true)
	require.InDelta(t, 10e-6, got.Seconds(), 2e-6)
}

func TestBusBWAsymptote(t *testing.T) {
	// Large message: measured busbw -> EffectiveBusBW.
	m := Model{LatencyUS: 5, Efficiency: 0.9, InterBytesPerSec: 50e9} // 50 GB/s line
	const n = 2
	size := int64(1 << 30) // 1 GiB
	d := m.OpTime(AllReduce, size, n, true)
	algbw := float64(size) / d.Seconds()
	busbw := algbw * BusBWFactor(AllReduce, n)
	want := m.EffectiveBusBW(true) // 45 GB/s
	require.InEpsilon(t, want, busbw, 0.01)
}

func TestEffectiveBusBWScope(t *testing.T) {
	m := Model{Efficiency: 0.5, IntraBytesPerSec: 100, InterBytesPerSec: 10}
	require.Equal(t, 50.0, m.EffectiveBusBW(false))
	require.Equal(t, 5.0, m.EffectiveBusBW(true))
}

func TestOpTimeIntraNode(t *testing.T) {
	// Intra-node uses IntraBytesPerSec. 1 GiB at 100 GB/s line, factor(AllReduce,2)=1.
	m := Model{LatencyUS: 0, Efficiency: 1.0, IntraBytesPerSec: 100e9, InterBytesPerSec: 1e9}
	size := int64(1 << 30)
	d := m.OpTime(AllReduce, size, 2, false)
	algbw := float64(size) / d.Seconds()
	busbw := algbw * BusBWFactor(AllReduce, 2)
	require.InEpsilon(t, m.EffectiveBusBW(false), busbw, 0.01)
}

func TestOpTimeZeroFactorIsLatency(t *testing.T) {
	m := Model{LatencyUS: 7, Efficiency: 1, InterBytesPerSec: 1e9}
	got := m.OpTime(AllReduce, 1024, 1, true) // n=1 => factor 0
	require.Equal(t, 7*time.Microsecond, got)
}
