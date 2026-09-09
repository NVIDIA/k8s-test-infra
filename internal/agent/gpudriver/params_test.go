// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The order a paramsFile renders is the order its params were declared in, not
// whatever order the map happens to iterate. Pinned because the file's whole
// contract is positional and a map alone would randomise it per run.
func TestParamsFile_RendersInDeclarationOrder(t *testing.T) {
	t.Parallel()

	f := newParamsFile([]param{
		{"ResmanDebugLevel", numericParam(-1)},
		{"DeviceFileMode", numericParam(0o666)},
		{"RegistryDwords", quotedParam("")},
	})

	require.Equal(t, "ResmanDebugLevel: 4294967295\nDeviceFileMode: 438\nRegistryDwords: \"\"\n", f.render())
}

// set has to reject a key the file does not carry rather than adding it. An
// appended key lands after the quoted params, where nvidia-modprobe can never
// reach it, so a typo'd override would look applied and be inert — the exact
// class of failure this surface already shipped once.
func TestParamsFile_SetRejectsUnknownParam(t *testing.T) {
	t.Parallel()

	f := newParamsFile([]param{{"DeviceFileMode", numericParam(0o666)}})

	require.Error(t, f.set("NVreg_DeviceFileMode", numericParam(0o660)))
	require.Equal(t, "DeviceFileMode: 438\n", f.render(), "a rejected set must not change the file")
}

func TestParamsFile_SetOverridesInPlace(t *testing.T) {
	t.Parallel()

	f := newParamsFile([]param{
		{"DeviceFileMode", numericParam(0o666)},
		{"RegistryDwords", quotedParam("")},
	})

	require.NoError(t, f.set("DeviceFileMode", numericParam(0o660)))

	require.Equal(t, "DeviceFileMode: 432\nRegistryDwords: \"\"\n", f.render(),
		"the overridden param keeps its position")
}
