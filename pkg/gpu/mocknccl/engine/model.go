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

import "time"

// Model holds resolved cost-model parameters. Bandwidths are in bytes/sec.
type Model struct {
	LatencyUS        float64
	Efficiency       float64
	IntraBytesPerSec float64 // NVLink aggregate per GPU
	InterBytesPerSec float64 // IB-derived (rate_gbps * hca_count)
}

// EffectiveBusBW is the target bus bandwidth (bytes/sec) for the scope, after
// applying efficiency. interNode selects the IB bottleneck; otherwise NVLink.
func (m Model) EffectiveBusBW(interNode bool) float64 {
	if interNode {
		return m.InterBytesPerSec * m.Efficiency
	}
	return m.IntraBytesPerSec * m.Efficiency
}

// OpTime returns the modeled duration of one collective call:
//
//	time = latency + msgBytes / algbw,  algbw = EffectiveBusBW / busbwFactor
//
// so a caller computing busbw = (msgBytes/time) * busbwFactor recovers
// EffectiveBusBW for large messages and is latency-bound for small ones.
// When the factor is 0 (n<=1), msgBytes<=0, or the effective bandwidth is 0,
// only latency applies.
func (m Model) OpTime(op Collective, msgBytes int64, n int, interNode bool) time.Duration {
	latency := time.Duration(m.LatencyUS * float64(time.Microsecond))
	factor := BusBWFactor(op, n)
	bus := m.EffectiveBusBW(interNode)
	if factor <= 0 || msgBytes <= 0 || bus <= 0 {
		return latency
	}
	algbw := bus / factor // bytes/sec
	transfer := time.Duration(float64(msgBytes) / algbw * float64(time.Second))
	return latency + transfer
}
