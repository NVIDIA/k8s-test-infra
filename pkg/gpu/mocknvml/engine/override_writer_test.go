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

func (w *fileOverrideWriter) mutate(index int, patch func(bucket map[string]any) error) error {
	doc, err := w.load()
	if err != nil {
		return err
	}
	if doc.Devices == nil {
		doc.Devices = map[string]map[string]any{}
	}
	key := strconv.Itoa(index)
	if doc.Devices[key] == nil {
		doc.Devices[key] = map[string]any{}
	}
	if err := patch(doc.Devices[key]); err != nil {
		return err
	}

	b, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(w.path, b, 0o644)
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
