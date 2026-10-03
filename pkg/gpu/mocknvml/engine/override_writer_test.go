// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// persistSetterWrites points the override store at a temp document and installs
// a writer that records setter writes in it, which is the arrangement the
// bridge sets up in production. Without it a setter has nowhere to write and
// every round trip below would assert against a device that refused the write.
//
// Both the store and the writer are process-wide, so a test using this cannot
// run in parallel with another that does.
func persistSetterWrites(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "overrides.yaml")

	// The clock never advances, so nothing here can pass by waiting out the
	// store's TTL: a getter sees a write only because the setter invalidated
	// the cache, which is what a consumer reading back its own write depends
	// on.
	frozen := time.Unix(0, 0)
	configOverrides = newConfigOverrideStoreAt(
		func() string { return path }, func() time.Time { return frozen })
	t.Cleanup(resetConfigOverrideStoreForTesting)

	SetOverrideWriter(&fileOverrideWriter{path: path})
	t.Cleanup(func() { SetOverrideWriter(nil) })
}

// fileOverrideWriter mirrors the writer the bridge installs, minus the flock a
// test's private temp file does not need. It exists so the engine's round trips
// run against a real document rather than a recording fake; the contract it
// stands in for — when a request counts as present, how an empty one is
// stored — is pinned by the mockctl tests that cover the production writer.
type fileOverrideWriter struct {
	path string
}

func (w *fileOverrideWriter) SetPowerLimit(index int, milliwatts uint32) error {
	return w.mutate(index, func(bucket map[string]any) error {
		deepMergeMaps(bucket, map[string]any{
			"power": map[string]any{"enforced_limit_mw": milliwatts},
		})
		return nil
	})
}

func (w *fileOverrideWriter) UpdateWorkloadProfiles(
	index int, apply func(base []uint32, present bool) ([]uint32, error),
) error {
	return w.mutate(index, func(bucket map[string]any) error {
		next, err := apply(requestedFromBucket(bucket))
		if err != nil {
			return err
		}
		if next == nil {
			next = []uint32{}
		}
		deepMergeMaps(bucket, map[string]any{
			"power": map[string]any{
				"workload_power_profiles": map[string]any{"requested": next},
			},
		})
		return nil
	})
}

func (w *fileOverrideWriter) SetNvlinkBwMode(index int, mode uint8, allDevices bool) error {
	patch := func(bucket map[string]any) error {
		deepMergeMaps(bucket, map[string]any{"nvlink_bw_mode": mode})
		return nil
	}
	if allDevices {
		// Mirrors mockctl.SetNvlinkBwMode: the write lands in the `all:`
		// bucket, and also drops the per-device copies, which would otherwise
		// outrank `all:` in the merge and mask it.
		return w.write(func(doc *ConfigOverrideDoc) (map[string]any, error) {
			for _, bucket := range doc.Devices {
				delete(bucket, "nvlink_bw_mode")
			}
			if doc.All == nil {
				doc.All = map[string]any{}
			}
			return doc.All, nil
		}, patch)
	}
	return w.mutate(index, patch)
}

func (w *fileOverrideWriter) SetNvlinkLowPowerThreshold(index int, threshold *uint32) error {
	return w.mutate(index, func(bucket map[string]any) error {
		if threshold == nil {
			delete(bucket, "nvlink_low_power_threshold")
			return nil
		}
		deepMergeMaps(bucket, map[string]any{"nvlink_low_power_threshold": *threshold})
		return nil
	})
}

func (w *fileOverrideWriter) mutate(index int, patch func(bucket map[string]any) error) error {
	return w.write(func(doc *ConfigOverrideDoc) (map[string]any, error) {
		if doc.Devices == nil {
			doc.Devices = map[string]map[string]any{}
		}
		key := strconv.Itoa(index)
		if doc.Devices[key] == nil {
			doc.Devices[key] = map[string]any{}
		}
		return doc.Devices[key], nil
	}, patch)
}

func (w *fileOverrideWriter) write(
	bucket func(*ConfigOverrideDoc) (map[string]any, error),
	patch func(map[string]any) error,
) error {
	doc, err := w.load()
	if err != nil {
		return err
	}
	b, err := bucket(doc)
	if err != nil {
		return err
	}
	if err := patch(b); err != nil {
		return err
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(w.path, out, 0o644)
}

func (w *fileOverrideWriter) load() (*ConfigOverrideDoc, error) {
	data, err := os.ReadFile(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return &ConfigOverrideDoc{}, nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := ParseConfigOverride(data)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return &ConfigOverrideDoc{}, nil
	}
	return doc, nil
}

// requestedFromBucket reads the request already recorded for a device, typed,
// and reports whether one was recorded at all.
func requestedFromBucket(bucket map[string]any) (requested []uint32, present bool) {
	merged, err := MergeDeviceConfig(&DeviceConfig{}, bucket)
	if err != nil || merged.Power == nil || merged.Power.WorkloadProfiles == nil ||
		merged.Power.WorkloadProfiles.Requested == nil {
		return nil, false
	}
	return merged.Power.WorkloadProfiles.Requested, true
}

// TestSetters_DeclineWithoutAWriter covers a consumer that links the engine
// without the bridge that installs a writer. Reporting success for a cap that
// was recorded nowhere is the failure this guards against.
func TestSetters_DeclineWithoutAWriter(t *testing.T) {
	dev := powerCappableDevice(t)

	require.Equal(t, nvml.ERROR_NO_PERMISSION, dev.SetPowerManagementLimit(250000),
		"a setter with nowhere to record the write must not report success")
}

// TestNvlinkSetters_DeclineWithoutAWriter covers the same contract for the
// NVLink setters. The bandwidth mode is gated on Blackwell and the threshold on
// Hopper, so each needs its own device to get past the gate and reach the
// writer lookup being asserted here.
func TestNvlinkSetters_DeclineWithoutAWriter(t *testing.T) {
	blackwell := bwDevice(t, "blackwell", bwFabric(t, &NVLinkConfig{}))
	require.Equal(t, nvml.ERROR_NO_PERMISSION, blackwell.SetMockNvlinkBwMode(3, false),
		"bandwidth mode with nowhere to record the write")

	hopper := bwDevice(t, "hopper", bwFabric(t, &NVLinkConfig{}))
	require.Equal(t, nvml.ERROR_NO_PERMISSION, hopper.SetMockNvLinkLowPowerThreshold(500),
		"low-power threshold with nowhere to record the write")

	require.Equal(t, nvml.ERROR_NO_PERMISSION, bwEngine(t, "hopper").SystemSetNvlinkBwMode(3),
		"node-wide bandwidth mode with nowhere to record the write")
}
