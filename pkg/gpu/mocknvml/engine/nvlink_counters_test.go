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
	"os"
	"path/filepath"
	"testing"
	"time"
)

// counterFabric builds a single-GPU fabric with one active switch link
// that accrues utilization counters at a positive rate.
func counterFabric(t *testing.T) *NodeFabric {
	t.Helper()
	yc := &YAMLConfig{
		System: SystemConfig{DriverVersion: "560.0", NumDevices: 1},
		Devices: []DeviceOverride{
			devWithBDF(0, "0000:0A:00.0"),
		},
		NVLink: &NVLinkConfig{
			Version:              5,
			BandwidthPerLinkGBPS: 100,
			Switches:             []NVSwitchConfig{{BDF: "0000:0F:00.0"}},
			Defaults:             &NVLinkDefaults{State: "active", DutyCycle: 0.05},
			DeviceLinks: []DeviceLinksConfig{
				{Index: 0, Links: switchLinks(1, "0000:0F:00.0")},
			},
		},
	}
	return BuildNodeFabric(&Config{NumDevices: 1, YAMLConfig: yc})
}

// TestNvLinkCounter_Monotonic asserts counter(t2) >= counter(t1) for t2 > t1.
func TestNvLinkCounter_Monotonic(t *testing.T) {
	f := counterFabric(t)
	epoch := time.Unix(1_000_000, 0)
	f.epoch = epoch

	var prev uint64
	for s := 0; s <= 60; s += 5 {
		now := epoch.Add(time.Duration(s) * time.Second)
		rx, tx := f.NvLinkCounters(0, 0, now)
		if rx != tx {
			t.Fatalf("rx (%d) != tx (%d)", rx, tx)
		}
		if rx < prev {
			t.Fatalf("counter went backwards at t=%ds: %d < %d", s, rx, prev)
		}
		prev = rx
	}
	if prev == 0 {
		t.Fatal("counter never grew; expected positive accrual")
	}
}

// TestNvLinkCounter_Deterministic asserts identical (epoch, now) yields the
// same value across independently constructed fabrics.
func TestNvLinkCounter_Deterministic(t *testing.T) {
	epoch := time.Unix(2_000_000, 0)
	now := epoch.Add(42 * time.Second)

	f1 := counterFabric(t)
	f1.epoch = epoch
	f2 := counterFabric(t)
	f2.epoch = epoch

	rx1, _ := f1.NvLinkCounters(0, 0, now)
	rx2, _ := f2.NvLinkCounters(0, 0, now)
	if rx1 != rx2 {
		t.Errorf("non-deterministic counters: %d vs %d", rx1, rx2)
	}
}

// TestNvLinkCounter_CrossProcessGrowth is the regression test for the
// in-process-counter trap: two fabrics with the SAME injected epoch but
// different now() (simulating two nvidia-smi invocations) must show the
// later one larger.
func TestNvLinkCounter_CrossProcessGrowth(t *testing.T) {
	epoch := time.Unix(3_000_000, 0)

	// First "nvidia-smi" process: sampled 10s after epoch.
	f1 := counterFabric(t)
	f1.epoch = epoch
	rx1, _ := f1.NvLinkCounters(0, 0, epoch.Add(10*time.Second))

	// Second "nvidia-smi" process: fresh fabric, same epoch, sampled 20s.
	f2 := counterFabric(t)
	f2.epoch = epoch
	rx2, _ := f2.NvLinkCounters(0, 0, epoch.Add(20*time.Second))

	if !(rx2 > rx1) {
		t.Errorf("cross-process growth failed: second sample %d not greater than first %d", rx2, rx1)
	}
}

// TestNvLinkCounter_InactiveLinkZero asserts inactive / missing links read 0.
func TestNvLinkCounter_InactiveLinkZero(t *testing.T) {
	f := counterFabric(t)
	f.epoch = time.Unix(1000, 0)
	now := f.epoch.Add(time.Hour)
	if rx, tx := f.NvLinkCounters(0, 5, now); rx != 0 || tx != 0 {
		t.Errorf("missing link counters: got rx=%d tx=%d, want 0/0", rx, tx)
	}
}

// TestNvLinkErrorCounter_ZeroByDefault asserts healthy links report no
// errors regardless of elapsed time.
func TestNvLinkErrorCounter_ZeroByDefault(t *testing.T) {
	f := counterFabric(t)
	f.epoch = time.Unix(1000, 0)
	now := f.epoch.Add(24 * time.Hour)
	if got := f.NvLinkErrorCount(0, 0, now); got != 0 {
		t.Errorf("error counter: got %d, want 0", got)
	}
}

// TestResolveCounterEpoch_FromEnv asserts MOCK_NVML_EPOCH takes precedence.
func TestResolveCounterEpoch_FromEnv(t *testing.T) {
	t.Setenv("MOCK_NVML_EPOCH", "1700000000")
	got := resolveCounterEpoch()
	if got.Unix() != 1700000000 {
		t.Errorf("resolveCounterEpoch from env: got %d, want 1700000000", got.Unix())
	}
}

// TestProcStatBtime parses btime from a /proc/stat-shaped fixture.
func TestProcStatBtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat")
	content := "cpu  1 2 3 4\nbtime 1234567890\nprocesses 99\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	v, ok := procStatBtime(path)
	if !ok || v != 1234567890 {
		t.Errorf("procStatBtime: got %d ok=%v, want 1234567890 true", v, ok)
	}

	if _, ok := procStatBtime(filepath.Join(dir, "nope")); ok {
		t.Error("procStatBtime on missing file: want ok=false")
	}
}
