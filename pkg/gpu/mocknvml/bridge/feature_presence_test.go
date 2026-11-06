// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestFeaturePresence_CExports(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "omitted"
		defaults := "  architecture: blackwell\n"
		want := nvml.ERROR_NOT_SUPPORTED
		if configured {
			name = "configured"
			defaults += "  gpu_operation_mode: {current: all_on}\n  retired_pages: {}\n" +
				"  ecc: {dram_encryption: {mode_current: disabled, mode_pending: disabled}}\n" +
				"  thermal: {target_temperature_c: 0}\n"
			want = nvml.SUCCESS
		}
		t.Run(name, func(t *testing.T) {
			b := newBridgeEngine(t, profile(driverWithCPER, 1, defaults))
			handle := b.handle(t, 0)
			for _, query := range []string{"gom", "retired_v1", "retired_v2", "retired_pending", "target", "dram"} {
				t.Run(query, func(t *testing.T) {
					value, ret := featureReadForTest(query, handle, false)
					require.Equal(t, want, ret)
					if configured {
						require.Zero(t, value)
					} else {
						require.Equal(t, uint32(0xdeadbeef), value, "errors must not write output")
					}
					_, ret = featureReadForTest(query, handle, true)
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "NULL output precedes feature support")
					_, ret = featureReadForTest(query, nil, false)
					require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "invalid handle precedes feature support")
				})
			}
			require.Equal(t, nvml.ERROR_ARGUMENT_VERSION_MISMATCH, dramVersionForTest(handle))
			b.fail(t, "lost")
			for _, query := range []string{"gom", "retired_v1", "retired_v2", "retired_pending", "target", "dram"} {
				value, ret := featureReadForTest(query, handle, false)
				require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret, query)
				require.Equal(t, uint32(0xdeadbeef), value, query)
			}
		})
	}
}

func TestSparseMode_InternalSlot(t *testing.T) {
	// nvidia-smi 580.65.06 reads sparse mode from byte offset 0x7c8.
	const slot = 249
	for _, tc := range []struct {
		name, defaults, override string
		want                     [2]uint32
		ret                      [2]nvml.Return
	}{
		{"omitted", "", "", [2]uint32{0xdeadbeef, 0xdeadbeef}, [2]nvml.Return{nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_NOT_SUPPORTED}},
		{"inherited_false", "  sparse_operation_mode: false\n", "", [2]uint32{0, 0}, [2]nvml.Return{nvml.SUCCESS, nvml.SUCCESS}},
		{"override_true", "", "    sparse_operation_mode: true\n", [2]uint32{0xdeadbeef, 1}, [2]nvml.Return{nvml.ERROR_NOT_SUPPORTED, nvml.SUCCESS}},
		{"override_false", "  sparse_operation_mode: true\n", "    sparse_operation_mode: false\n", [2]uint32{1, 0}, [2]nvml.Return{nvml.SUCCESS, nvml.SUCCESS}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBridgeEngine(t, profile(driverWithCPER, 2, "  architecture: hopper\n"+tc.defaults)+
				"devices:\n  - index: 0\n  - index: 1\n"+tc.override)
			for _, index := range []int{1, 0} {
				handle := b.handle(t, index)
				value, ret := internalSlotReadForTest(slot, handle)
				require.Equal(t, tc.ret[index], ret)
				require.Equal(t, tc.want[index], value)
				require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, internalSlotCallForTest(slot, handle, 0))
			}
			_, ret := internalSlotReadForTest(slot, nil)
			require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret)
			handle := b.handle(t, 0)
			b.fail(t, "lost")
			value, ret := internalSlotReadForTest(slot, handle)
			require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
			require.Equal(t, uint32(0xdeadbeef), value)
		})
	}
}
