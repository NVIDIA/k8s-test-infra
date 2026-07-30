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
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func a100ProfilePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "deployments", "nvml-mock", "helm",
		"nvml-mock", "profiles", "a100.yaml")
}

func TestDevicesFromConfig_ResolvesIndexUUIDAndMemory(t *testing.T) {
	yamlCfg, err := engine.LoadYAMLConfig(a100ProfilePath(t))
	require.NoError(t, err, "load a100 profile")
	cfg := &engine.Config{YAMLConfig: yamlCfg, NumDevices: len(yamlCfg.Devices)}

	devices := DevicesFromConfig(cfg)

	require.Len(t, devices, 8, "the a100 profile advertises 8 GPUs")
	require.Equal(t, 0, devices[0].Index)
	require.Equal(t, a100Total, devices[0].TotalBytes,
		"total must come from the profile, not a hard-coded constant")
	require.Equal(t, a100Reserved, devices[0].ReservedBytes)

	seen := map[string]bool{}
	for _, d := range devices {
		require.NotEmpty(t, d.UUID, "device %d has no UUID; claims key on it", d.Index)
		require.False(t, seen[d.UUID], "duplicate UUID %s would merge two devices' claims", d.UUID)
		seen[d.UUID] = true
	}
}

// The invariant that lets the watcher coexist with `nvml-mock-ctl set`: both do
// a read-modify-write of one file, so publishing memory must not drop anything
// a human put there.
func TestPublish_PreservesUnrelatedOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")

	// What `nvml-mock-ctl temp --gpu 0 85` leaves behind.
	existing := &mockctl.Doc{}
	existing.SetFields(mockctl.Target{Index: 0}, map[string]any{
		"thermal": map[string]any{"temperature_gpu_c": 85},
	})
	existing.SetFields(mockctl.Target{All: true}, map[string]any{
		"performance_state": "P8",
	})
	require.NoError(t, mockctl.WriteAtomic(path, existing))

	require.NoError(t, Publish(path, []Reading{{Index: 0, UsedBytes: 4096, FreeBytes: 8192}}))

	got, err := mockctl.Load(path)
	require.NoError(t, err)

	dev0 := got.Devices["0"]
	require.NotNil(t, dev0["thermal"],
		"publishing memory wiped a concurrent nvml-mock-ctl temperature override")
	require.Equal(t, "P8", got.All["performance_state"],
		"publishing memory wiped the `all` bucket")

	mem, ok := dev0["memory"].(map[string]any)
	require.True(t, ok, "memory block missing after publish")
	require.EqualValues(t, 4096, mem["used_bytes"])
	require.EqualValues(t, 8192, mem["free_bytes"])
}

func TestPublish_OverwritesItsOwnPreviousReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")

	require.NoError(t, Publish(path, []Reading{{Index: 0, UsedBytes: 9999, FreeBytes: 1}}))
	require.NoError(t, Publish(path, []Reading{{Index: 0, UsedBytes: 0, FreeBytes: 10000}}))

	got, err := mockctl.Load(path)
	require.NoError(t, err)
	mem := got.Devices["0"]["memory"].(map[string]any)
	require.EqualValues(t, 0, mem["used_bytes"],
		"a released GPU must fall back to 0; a merge that only grows would pin the busy value")
	require.EqualValues(t, 10000, mem["free_bytes"])
}

// stubLister lets the loop be driven without a kubelet.
type stubLister struct {
	claims []Claim
	err    error
	calls  int
}

func (s *stubLister) List(context.Context) ([]Claim, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.claims, nil
}
func (s *stubLister) Close() error { return nil }

func TestWatcher_PublishesOnEachTick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	lister := &stubLister{claims: []Claim{{DeviceUUID: "GPU-aaa"}}}

	w := &Watcher{
		Lister:       lister,
		Devices:      a100Devices(),
		Policy:       DefaultPolicy(),
		OverridePath: path,
		Interval:     5 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	require.NoError(t, w.Run(ctx), "a cancelled context is a clean shutdown, not an error")

	require.Greater(t, lister.calls, 1, "the watcher polled only once; it is not a loop")

	got, err := mockctl.Load(path)
	require.NoError(t, err)
	mem := got.Devices["0"]["memory"].(map[string]any)
	require.EqualValues(t, (a100Total-a100Reserved)/2, mem["used_bytes"],
		"the claim on GPU-aaa did not reach the override file")
}

// A kubelet blip must not kill the watcher or freeze the readings: the next
// poll re-establishes truth. Exiting on the first error would leave every GPU
// pinned at its last value for the life of the pod.
func TestWatcher_SurvivesAListerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	lister := &stubLister{err: errors.New("kubelet restarting")}

	w := &Watcher{
		Lister:       lister,
		Devices:      a100Devices(),
		Policy:       DefaultPolicy(),
		OverridePath: path,
		Interval:     5 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.NoError(t, w.Run(ctx))

	require.Greater(t, lister.calls, 1, "the watcher gave up after the first error")

	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err),
		"a failed list must publish nothing; writing zeros would report every GPU idle "+
			"whenever the kubelet is briefly unreachable")
}
