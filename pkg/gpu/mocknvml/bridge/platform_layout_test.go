// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// nvidia-smi renders the GPU Fabric GUID from the first eight bytes of ibGuid,
// little-endian, in either struct version. A byte-order or offset slip would
// render a GUID that matches neither the PDI nor what an NVLink switch's
// inventory lists for the GPU.
func TestDeviceGetPlatformInfo_FabricGUIDIsPDI(t *testing.T) {
	t.Setenv("MOCK_NVML_CONFIG", filepath.Join("..", "..", "..", "..", "deployments", "nvml-mock", "helm", "nvml-mock", "profiles", "gb200.yaml"))
	t.Setenv("MOCK_NVML_OVERRIDES", filepath.Join(t.TempDir(), "overrides.yaml"))
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)
	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	e.SetVisibleDevicesForTesting(nil)

	seen := map[uint64]int{}
	for index := range 2 {
		handle, ret := e.DeviceGetHandleByIndex(index)
		require.Equal(t, nvml.SUCCESS, ret)
		pdi, ret32 := pdiForTest(handle, nvml.STRUCT_VERSION(nvml.Pdi{}, 1))
		require.Equal(t, uint32(nvml.SUCCESS), ret32, "device %d PDI", index)

		for _, version := range []uint32{
			nvml.STRUCT_VERSION(nvml.PlatformInfo_v1{}, 1),
			nvml.STRUCT_VERSION(nvml.PlatformInfo_v2{}, 2),
		} {
			ib, ret32 := platformIbGUIDForTest(handle, version)
			require.Equal(t, uint32(nvml.SUCCESS), ret32, "device %d version %#x", index, version)
			require.Equal(t, pdi, binary.LittleEndian.Uint64(ib[:8]), "device %d version %#x: fabric GUID", index, version)
			require.Equal(t, make([]byte, 8), ib[8:], "device %d version %#x: bytes past the GUID", index, version)
		}
		if prev, dup := seen[pdi]; dup {
			t.Fatalf("devices %d and %d share fabric GUID %#016x", prev, index, pdi)
		}
		seen[pdi] = index
	}
}
