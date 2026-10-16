// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func TestHostMaxPcieSlot_UsesOnlyExplicitHostValue(t *testing.T) {
	for _, tc := range []struct {
		profile string
		want    uint32
		ret     nvml.Return
	}{
		{"a100", 99, nvml.ERROR_NOT_SUPPORTED},
		{"b200", 99, nvml.ERROR_NOT_SUPPORTED},
		{"gb200", 99, nvml.ERROR_NOT_SUPPORTED},
		{"gb300", 4, nvml.SUCCESS},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			t.Setenv("MOCK_NVML_CONFIG", filepath.Join("..", "configs", "mock-nvml-config-"+tc.profile+".yaml"))
			t.Setenv("MOCK_NVML_OVERRIDES", filepath.Join(t.TempDir(), "overrides.yaml"))
			engine.ResetForTesting()
			t.Cleanup(engine.ResetForTesting)
			e := engine.GetEngine()
			require.Equal(t, nvml.SUCCESS, e.Init())
			t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
			handle, ret := e.DeviceGetHandleByIndex(0)
			require.Equal(t, nvml.SUCCESS, ret)

			value, ret := hostMaxPcieLinkGenForTest(handle, 99)
			require.Equal(t, tc.ret, ret)
			require.Equal(t, tc.want, value)

			unknown := unsafe.Pointer(new(byte))
			require.Nil(t, e.LookupConfigurableDevice(unknown))
			value, ret = hostMaxPcieLinkGenLookupForTest(unknown, 99)
			require.Equal(t, nvml.SUCCESS, ret, "unknown handles retain the historical result")
			require.Zero(t, value, "a reused output buffer must not keep stale data")
		})
	}
}
