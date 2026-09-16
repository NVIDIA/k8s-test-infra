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
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestDeriveDriverBranch(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"580.65.06":  "r580_00",
		"570.148.08": "r570_00",
		"550.163":    "r550_00",
		"470.256.02": "r470_00",
		// NVML documents the branch as an alphanumeric string with no fallback,
		// so an unparseable version yields no branch rather than a fabricated one.
		"":        "",
		"unknown": "",
		"r570_40": "",
		".580":    "",
	}
	for version, want := range cases {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, deriveDriverBranch(version))
		})
	}
}

func TestSystemGetDriverBranch_DerivedFromDriverVersion(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{NumDevices: 1, DriverVersion: "580.65.06"})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })

	branch, ret := e.SystemGetDriverBranch()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "r580_00", branch)
}

// TestSystemGetDriverBranch_ExplicitOverridesDerivation is the reason the key
// exists: a point-release branch such as r570_40 cannot be derived from the
// version string, so a profile that models one has to state it.
func TestSystemGetDriverBranch_ExplicitOverridesDerivation(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{
		NumDevices:    1,
		DriverVersion: "570.148.08",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "570.148.08",
				NumDevices:    1,
				DriverBranch:  "r570_40",
			},
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })

	branch, ret := e.SystemGetDriverBranch()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "r570_40", branch)
}

// TestSystemGetDriverBranch_BeforeInit matches SystemGetDriverVersion:
// nvidia-smi reads system strings during startup, before nvmlInit completes.
func TestSystemGetDriverBranch_BeforeInit(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{NumDevices: 1, DriverVersion: "580.65.06"})

	branch, ret := e.SystemGetDriverBranch()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, "r580_00", branch)
}

// TestSystemGetHicVersion_NoneConfigured is the case every modern node hits:
// HICs belong to the retired S-class enclosures, and NVML's documented return
// set for this call has no NOT_SUPPORTED.
func TestSystemGetHicVersion_NoneConfigured(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{NumDevices: 1, DriverVersion: "580.65.06"})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })

	entries, ret := e.SystemGetHicVersion()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, entries)
}

func TestSystemGetHicVersion_ReportsConfiguredEntries(t *testing.T) {
	t.Parallel()
	e := hicEngine(t, []HICConfig{
		{ID: 0, FirmwareVersion: "1.2.3.4"},
		{ID: 1, FirmwareVersion: "5.6.7.8"},
	})

	entries, ret := e.SystemGetHicVersion()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []HICEntry{
		{ID: 0, FirmwareVersion: "1.2.3.4"},
		{ID: 1, FirmwareVersion: "5.6.7.8"},
	}, entries)
}

// TestSystemGetHicVersion_TruncatesOverlongFirmware keeps a profile typo from
// overrunning the fixed firmwareVersion array in nvmlHwbcEntry_t.
func TestSystemGetHicVersion_TruncatesOverlongFirmware(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("9", HICFirmwareVersionMaxLen+10)
	e := hicEngine(t, []HICConfig{{ID: 7, FirmwareVersion: long}})

	entries, ret := e.SystemGetHicVersion()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].FirmwareVersion, HICFirmwareVersionMaxLen)
	require.Equal(t, uint32(7), entries[0].ID)
}

func hicEngine(t *testing.T, hic []HICConfig) *Engine {
	t.Helper()
	e := NewEngine(&Config{
		NumDevices:    1,
		DriverVersion: "580.65.06",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System: SystemConfig{
				DriverVersion: "580.65.06",
				NumDevices:    1,
				HIC:           hic,
			},
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	return e
}
