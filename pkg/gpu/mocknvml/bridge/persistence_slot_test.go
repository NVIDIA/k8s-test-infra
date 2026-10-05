// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// `nvidia-smi -pm` sets persistence mode through export-table slot 251, never
// through nvmlDeviceSetPersistenceMode, so the slot has to record the write
// for the next nvidia-smi process to read it back.
func TestPersistenceModeSlot_RecordsTheWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	t.Setenv("MOCK_NVML_NUM_DEVICES", "2")
	t.Setenv("MOCK_NVML_CONFIG", "")
	t.Setenv("MOCK_NVML_OVERRIDES", path)
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)
	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	h, ret := e.DeviceGetHandleByIndex(1)
	require.Equal(t, nvml.SUCCESS, ret)

	for _, tc := range []struct {
		mode nvml.EnableState
		want string
	}{
		{nvml.FEATURE_DISABLED, "disabled"},
		{nvml.FEATURE_ENABLED, "enabled"},
	} {
		require.Equal(t, nvml.SUCCESS, internalSlotCallForTest(persistenceModeSlotForTest, h, uintptr(tc.mode)))
		doc, err := mockctl.Load(path)
		require.NoError(t, err)
		require.Equal(t, tc.want, doc.Devices["1"]["persistence_mode"])
		require.NotContains(t, doc.Devices, "0", "only the addressed GPU changes")
	}
}
