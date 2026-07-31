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

// Package allocwatch turns Kubernetes GPU allocation into mock NVML memory
// readings.
//
// Issue #506 asks that `memory.used_bytes` respond to allocation: a pod holding
// an nvidia.com/gpu claim should move the number, and the number should return
// when the pod goes away. Nothing in the deployment observed allocation, so
// every profile reported a constant zero and any dashboard built on the mock
// was visibly fake.
//
// The write path already existed. The engine hot-reloads
// <driver_root>/config/overrides.yaml within one TTL, and that file sits beside
// the config the DaemonSet stages into every injected container. This package
// supplies the missing producer: it reads the node's current allocation from
// the kubelet pod-resources API and renders the memory half of that override
// document.
//
// The design is deliberately level-triggered. Every poll recomputes all
// readings from the full current allocation set rather than applying deltas, so
// a missed or duplicated event cannot accumulate drift, and "returns when the
// pod is removed" needs no separate teardown path — it is what an empty claim
// list already computes.
//
// What this does NOT claim: the byte count is synthetic. The mock runs no
// kernel and allocates no device memory, so the number reflects that a claim
// EXISTS, not what a workload actually touched. See the Metric Fidelity section
// of docs/configuration.md.
package allocwatch

// Device is one GPU the mock exposes on this node, as resolved from the loaded
// profile. UUID is the identifier the device plugin advertises and therefore
// the key pod-resources reports claims against.
type Device struct {
	Index         int
	UUID          string
	TotalBytes    uint64
	ReservedBytes uint64
}

// Claim is a single container's hold on one device. The same DeviceUUID appears
// once per holding container, so time-sliced GPUs shared by several containers
// produce several claims.
type Claim struct {
	DeviceUUID string

	// Pod, Namespace and Container identify the holder. They are unused by the
	// memory reconciliation and carried for logging and for the `processes`
	// surface tracked separately in #506 item 2.
	Namespace string
	Pod       string
	Container string
}

// Policy tunes how much memory a single claim is reported to consume.
type Policy struct {
	// UsedFractionPerClaim is the share of the usable aperture
	// (total - reserved) attributed to each claim. Claims on one device add up
	// and clamp at the aperture.
	UsedFractionPerClaim float64
}

// DefaultPolicy attributes half the usable aperture per claim, so a single
// exclusive claim reads as a substantial but not saturated card, and two
// time-sliced containers fill it. The value is arbitrary in the sense that no
// real allocation backs it; it is chosen to look plausible on a dashboard and
// to leave headroom that a second claim visibly consumes.
func DefaultPolicy() Policy {
	return Policy{UsedFractionPerClaim: 0.5}
}

// Reading is the reconciled memory state of one device.
type Reading struct {
	Index     int
	UsedBytes uint64
	FreeBytes uint64
}

// Reconcile computes a Reading for every device from the full current claim
// set. Devices with no claim are included and report the idle values, so the
// emitted override always covers the whole node: writing only the busy devices
// would leave a previously busy GPU pinned at its last value once its pod went
// away.
func Reconcile(devices []Device, claims []Claim, policy Policy) []Reading {
	claimsByUUID := make(map[string]int, len(devices))
	for _, c := range claims {
		claimsByUUID[c.DeviceUUID]++
	}

	readings := make([]Reading, 0, len(devices))
	for _, d := range devices {
		aperture := d.TotalBytes - d.ReservedBytes
		if d.ReservedBytes > d.TotalBytes {
			aperture = 0
		}

		perClaim := uint64(float64(aperture) * policy.UsedFractionPerClaim)
		used := perClaim * uint64(claimsByUUID[d.UUID])
		if used > aperture {
			// Oversubscription (time slicing beyond the policy share) clamps at
			// the aperture: a card cannot hold more than it has, and letting
			// used exceed aperture would underflow the free calculation below.
			used = aperture
		}

		readings = append(readings, Reading{
			Index:     d.Index,
			UsedBytes: used,
			FreeBytes: aperture - used,
		})
	}
	return readings
}

// OverrideDoc renders readings as the `devices` map of a config override
// document, matching what `nvml-mock-ctl set --gpu N memory.used_bytes=...`
// writes.
//
// Only used_bytes and free_bytes are emitted. total_bytes is a hardware fact
// that allocation does not change, and emitting any non-memory key would
// clobber a concurrent nvml-mock-ctl override of temperature, utilization or
// failure injection when the two documents merge.
func OverrideDoc(readings []Reading) map[string]any {
	devices := make(map[string]any, len(readings))
	for _, r := range readings {
		devices[itoa(r.Index)] = map[string]any{
			"memory": map[string]any{
				"used_bytes": r.UsedBytes,
				"free_bytes": r.FreeBytes,
			},
		}
	}
	return map[string]any{"devices": devices}
}

// itoa avoids pulling strconv in for a value that is always a small
// non-negative device index.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
