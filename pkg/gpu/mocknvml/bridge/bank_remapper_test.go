// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package main

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// rubinBankRemapperProfile is a Rubin board on the first driver exporting the
// bank remapper, with one remap waiting and a bank group out of spares.
func rubinBankRemapperProfile(driverVersion string) string {
	return profile(driverVersion, 1, "  architecture: rubin\n"+
		"  bank_remapper:\n"+
		"    active_remappings: 3\n"+
		"    inactive_remappings: 1\n"+
		"    pending: true\n"+
		"    histogram:\n"+
		"      max_spare_groups: 250\n"+
		"      no_spare_groups: 2\n")
}

// Every field of the C struct must be written, the histogram included: a
// consumer reads the whole struct, and the sentinel the hook fills it with would
// show through any field the export left alone.
func TestBankRemapperStatus_WritesEveryField(t *testing.T) {
	b := newBridgeEngine(t, rubinBankRemapperProfile("615.71.09"))

	status, ret := bankRemapperStatusForTest(b.handle(t, 0))
	require.Equal(t, uint32(nvml.SUCCESS), ret)
	require.Equal(t, nvml.EccBankRemapperStatus_v1{
		ActiveRemappings:   3,
		InactiveRemappings: 1,
		BPending:           1,
		Histogram:          nvml.EccBankRemapperHistogram_v1{MaxSpareGroupCount: 250, NoSpareGroupCount: 2},
	}, status)
}

// No driver before 615 exports the call, so a consumer on an older profile
// must be told the symbol is absent.
func TestBankRemapperStatus_FunctionNotFoundBefore615(t *testing.T) {
	b := newBridgeEngine(t, rubinBankRemapperProfile(driverWithHostname))

	_, ret := bankRemapperStatusForTest(b.handle(t, 0))
	require.Equal(t, uint32(nvml.ERROR_FUNCTION_NOT_FOUND), ret)
}

func TestBankRemapperStatus_InvalidDevice(t *testing.T) {
	newBridgeEngine(t, rubinBankRemapperProfile("615.71.09"))

	_, ret := bankRemapperStatusForTest(nil)
	require.Equal(t, uint32(nvml.ERROR_INVALID_ARGUMENT), ret)
}
