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

package allocwatch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A100 40 GiB, transcribed from the shipped a100 profile rather than computed,
// so a change to either the profile or the arithmetic shows up as a diff here.
const (
	a100Total    uint64 = 42949672960
	a100Reserved uint64 = 611319808
	a100Idle     uint64 = 42338353152 // total - reserved, the profile's free_bytes
)

func a100Devices() []Device {
	return []Device{
		{Index: 0, UUID: "GPU-aaa", TotalBytes: a100Total, ReservedBytes: a100Reserved},
		{Index: 1, UUID: "GPU-bbb", TotalBytes: a100Total, ReservedBytes: a100Reserved},
	}
}

func TestReconcile_UnallocatedNodeReportsIdle(t *testing.T) {
	got := Reconcile(a100Devices(), nil, DefaultPolicy())

	require.Len(t, got, 2, "every device must be represented, not just the busy ones")
	require.Equal(t, uint64(0), got[0].UsedBytes, "an unallocated GPU must report zero used")
	require.Equal(t, a100Idle, got[0].FreeBytes, "an unallocated GPU must report the idle free value")
	require.Equal(t, uint64(0), got[1].UsedBytes)
}

func TestReconcile_OneClaimMovesOnlyItsOwnDevice(t *testing.T) {
	got := Reconcile(a100Devices(), []Claim{{DeviceUUID: "GPU-bbb"}}, DefaultPolicy())

	// Half the usable aperture per claim, the documented default policy.
	wantUsed := (a100Total - a100Reserved) / 2
	require.Equal(t, wantUsed, got[1].UsedBytes, "the claimed GPU must report the per-claim share")
	require.Equal(t, a100Idle-wantUsed, got[1].FreeBytes, "free must fall by exactly what used gained")

	require.Equal(t, uint64(0), got[0].UsedBytes,
		"GPU 0 holds no claim; a claim on GPU 1 must not move it. Reporting node-wide "+
			"totals on every device is the bug this pins")
	require.Equal(t, a100Idle, got[0].FreeBytes)
}

// Time-slicing: the device plugin advertises one physical GPU to several
// containers, so pod-resources reports the same device UUID more than once.
func TestReconcile_ClaimsOnOneDeviceAccumulate(t *testing.T) {
	claims := []Claim{{DeviceUUID: "GPU-aaa"}, {DeviceUUID: "GPU-aaa"}}
	got := Reconcile(a100Devices(), claims, DefaultPolicy())

	require.Equal(t, a100Total-a100Reserved, got[0].UsedBytes,
		"two claims at half the aperture each must fill it; deduplicating the device "+
			"list would report half and hide the second container")
	require.Equal(t, uint64(0), got[0].FreeBytes)
}

// The saturation guard: more claims than the card can hold must not report more
// memory in use than the card physically has, and must never underflow free.
func TestReconcile_OversubscriptionClampsAtCapacity(t *testing.T) {
	claims := []Claim{{DeviceUUID: "GPU-aaa"}, {DeviceUUID: "GPU-aaa"}, {DeviceUUID: "GPU-aaa"}}
	got := Reconcile(a100Devices(), claims, DefaultPolicy())

	require.Equal(t, a100Total-a100Reserved, got[0].UsedBytes,
		"three half-aperture claims must clamp at the usable aperture, not exceed it")
	require.Equal(t, uint64(0), got[0].FreeBytes,
		"free must clamp at zero rather than wrap around through uint64")
	require.LessOrEqual(t, got[0].UsedBytes, a100Total,
		"used must never exceed the physical card size")
}

// A claim naming a device this node does not have (a stale kubelet entry, or a
// GPU from a profile that shrank) must be dropped, not silently attributed to
// device 0.
func TestReconcile_UnknownDeviceUUIDIsIgnored(t *testing.T) {
	claims := []Claim{{DeviceUUID: "GPU-does-not-exist"}}
	got := Reconcile(a100Devices(), claims, DefaultPolicy())

	require.Equal(t, uint64(0), got[0].UsedBytes,
		"an unmatched claim must not land on the first device")
	require.Equal(t, uint64(0), got[1].UsedBytes)
}

func TestReconcile_ReleaseReturnsToIdle(t *testing.T) {
	devices := a100Devices()
	busy := Reconcile(devices, []Claim{{DeviceUUID: "GPU-aaa"}}, DefaultPolicy())
	require.Positive(t, busy[0].UsedBytes, "precondition: the claim moved the number")

	// The whole point of polling full state rather than tracking deltas: the
	// released reading is computed from scratch, so it cannot drift.
	idle := Reconcile(devices, nil, DefaultPolicy())
	require.Equal(t, uint64(0), idle[0].UsedBytes, "removing the claim must return used to zero")
	require.Equal(t, a100Idle, idle[0].FreeBytes, "removing the claim must restore idle free")
}

func TestReconcile_PolicyFractionIsHonoured(t *testing.T) {
	quarter := Policy{UsedFractionPerClaim: 0.25}
	got := Reconcile(a100Devices(), []Claim{{DeviceUUID: "GPU-aaa"}}, quarter)

	require.Equal(t, (a100Total-a100Reserved)/4, got[0].UsedBytes,
		"a 0.25 policy must report a quarter of the aperture, proving the fraction is "+
			"read rather than the default being hard-coded")
}

func TestOverrideDoc_EmitsOnlyMemoryFields(t *testing.T) {
	readings := Reconcile(a100Devices(), []Claim{{DeviceUUID: "GPU-aaa"}}, DefaultPolicy())
	doc := OverrideDoc(readings)

	require.Contains(t, doc, "devices")
	dev0, ok := doc["devices"].(map[string]any)["0"].(map[string]any)
	require.True(t, ok, "device 0 must be present in the override document")

	mem, ok := dev0["memory"].(map[string]any)
	require.True(t, ok, "device 0 must carry a memory block")
	require.Len(t, dev0, 1,
		"the watcher must write ONLY memory; writing any other key would clobber a "+
			"concurrent `nvml-mock-ctl set` of temperature or utilization")
	require.ElementsMatch(t, []string{"used_bytes", "free_bytes"}, keysOf(mem),
		"total_bytes must not be rewritten: it is a hardware fact, not an allocation")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
