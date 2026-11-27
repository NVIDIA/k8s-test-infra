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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestHexID_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		json    string
		want    uint64
		wantErr string
	}{
		"as nvidia-smi prints it": {json: `"0x00000000000000ab"`, want: 0xab},
		"short":                   {json: `"0xab"`, want: 0xab},
		"upper case":              {json: `"0XAB"`, want: 0xab},
		"all 64 bits":             {json: `"0xffffffffffffffff"`, want: 0xffffffffffffffff},
		"no 0x prefix":            {json: `"ab"`, wantErr: "must start with 0x"},
		"no digits":               {json: `"0x"`, wantErr: "at most 16 hex digits"},
		"over 64 bits":            {json: `"0x1ffffffffffffffff"`, wantErr: "at most 16 hex digits"},
		"not hex":                 {json: `"0xzz"`, wantErr: "at most 16 hex digits"},
		// What YAML hands over for an unquoted 0xab.
		"a number": {json: `171`, wantErr: "quoted hex string"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var got HexID
			err := json.Unmarshal([]byte(tc.json), &got)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, uint64(got))
		})
	}
}

func TestFabricGUIDConfig_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		json    string
		want    FabricGUIDConfig
		wantErr string
	}{
		"pdi":         {json: `"pdi"`, want: FabricGUIDConfig{FromPDI: true}},
		"PDI":         {json: `"PDI"`, want: FabricGUIDConfig{FromPDI: true}},
		"fixed value": {json: `"0x00000000000000ab"`, want: FabricGUIDConfig{Value: 0xab}},
		"zero":        {json: `"0x0"`, want: FabricGUIDConfig{}},
		"misspelled":  {json: `"pid"`, wantErr: `or "pdi"`},
		"a number":    {json: `171`, wantErr: "quoted hex string"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var got FabricGUIDConfig
			err := json.Unmarshal([]byte(tc.json), &got)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// Every refresh decodes the effective config back out of JSON, so a value that
// did not survive the round trip would vanish the first time an override
// document existed, whatever it said.
func TestMergeDeviceConfig_KeepsIdentifiers(t *testing.T) {
	t.Parallel()
	pdi := HexID(0x1234)
	base := &DeviceConfig{
		PDI:      &pdi,
		Platform: &PlatformConfig{ModuleID: 1, GPUFabricGUID: &FabricGUIDConfig{FromPDI: true}},
	}

	merged, err := MergeDeviceConfig(base, nil)
	require.NoError(t, err)
	require.Equal(t, base, merged)

	merged, err = MergeDeviceConfig(base, map[string]any{
		"pdi":      "0x00000000000000ab",
		"platform": map[string]any{"gpu_fabric_guid": "0x00000000000000cd"},
	})
	require.NoError(t, err)
	require.Equal(t, HexID(0xab), *merged.PDI, "runtime pdi")
	require.Equal(t, FabricGUIDConfig{Value: 0xcd}, *merged.Platform.GPUFabricGUID, "runtime fabric GUID")
	require.Equal(t, uint8(1), merged.Platform.ModuleID, "fields the patch left alone")
}

func writeIdentifierProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`version: "1.0"
system:
  driver_version: "580.65.06"
`+body), 0o600))
	return path
}

func TestLoadYAMLConfig_Identifiers(t *testing.T) {
	t.Parallel()
	cfg, err := LoadYAMLConfig(writeIdentifierProfile(t, `
device_defaults:
  platform:
    gpu_fabric_guid: "pdi"
devices:
  - index: 0
    pdi: "0x00000000000000a0"
  - index: 1
    pdi: "0x00000000000000a1"
    platform:
      gpu_fabric_guid: "0x00000000000000b1"
`))
	require.NoError(t, err)
	merged := &Config{YAMLConfig: cfg}

	first := merged.GetDeviceConfig(0)
	require.Equal(t, HexID(0xa0), *first.PDI)
	require.Equal(t, FabricGUIDConfig{FromPDI: true}, *first.Platform.GPUFabricGUID, "inherited from device_defaults")

	second := merged.GetDeviceConfig(1)
	require.Equal(t, HexID(0xa1), *second.PDI)
	require.Equal(t, FabricGUIDConfig{Value: 0xb1}, *second.Platform.GPUFabricGUID, "per-device value")
}

func TestLoadYAMLConfig_RejectsUnusableIdentifiers(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		body    string
		wantErr string
	}{
		"unquoted pdi": {
			body:    "device_defaults:\n  pdi: 0xab\n",
			wantErr: "quoted hex string",
		},
		"one pdi for every GPU": {
			body:    "devices:\n  - index: 0\n  - index: 1\ndevice_defaults:\n  pdi: \"0xab\"\n",
			wantErr: "duplicate pdi",
		},
		"one fabric GUID for every GPU": {
			body: "devices:\n  - index: 0\n  - index: 1\n" +
				"device_defaults:\n  platform:\n    gpu_fabric_guid: \"0xab\"\n",
			wantErr: "duplicate platform.gpu_fabric_guid",
		},
		// One GPU's fixed GUID is the PDI another reports through "pdi".
		"fixed GUID equals another GPU's set PDI": {
			body: "devices:\n" +
				"  - index: 0\n    pdi: \"0xa0\"\n    platform:\n      gpu_fabric_guid: \"0xa1\"\n" +
				"  - index: 1\n    pdi: \"0xa1\"\n    platform:\n      gpu_fabric_guid: \"pdi\"\n",
			wantErr: "duplicate platform.gpu_fabric_guid",
		},
		"fixed GUID equals another GPU's derived PDI": {
			body: "devices:\n" +
				"  - index: 0\n    uuid: \"GPU-11111111-0000-0000-0000-000000000000\"\n" +
				"    platform:\n      gpu_fabric_guid: \"pdi\"\n" +
				"  - index: 1\n    platform:\n      gpu_fabric_guid: \"" +
				formatHexID(pdiFromUUID("GPU-11111111-0000-0000-0000-000000000000")) + "\"\n",
			wantErr: "duplicate platform.gpu_fabric_guid",
		},
		"fixed PDI equals another GPU's derived PDI": {
			body: "devices:\n" +
				"  - index: 0\n    uuid: \"GPU-11111111-0000-0000-0000-000000000000\"\n" +
				"  - index: 1\n    pdi: \"" +
				formatHexID(pdiFromUUID("GPU-11111111-0000-0000-0000-000000000000")) + "\"\n",
			wantErr: "duplicate pdi",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadYAMLConfig(writeIdentifierProfile(t, tc.body))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// A GPU with no declared UUID gets the base mock's, which is only chosen at
// startup, so its PDI cannot be compared at load. Such a config still loads.
func TestLoadYAMLConfig_AcceptsPDIBackedGUIDsWithoutUUIDs(t *testing.T) {
	t.Parallel()
	_, err := LoadYAMLConfig(writeIdentifierProfile(t,
		"devices:\n  - index: 0\n  - index: 1\n"+
			"device_defaults:\n  platform:\n    gpu_fabric_guid: \"pdi\"\n"))
	require.NoError(t, err)
}

func TestGetPdi_ReportsConfiguredValue(t *testing.T) {
	t.Parallel()
	pdi := HexID(0x00c0ffee00c0ffee)
	got, ret := makePlatformDevice(t, &DeviceConfig{PDI: &pdi}).GetPdi()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(pdi), got.Value)
}

func TestGetMockPlatformInfo_FabricGUID(t *testing.T) {
	t.Parallel()
	configured := HexID(0x00c0ffee00c0ffee)
	for name, tc := range map[string]struct {
		pdi  *HexID
		guid *FabricGUIDConfig
		want func(derivedPDI uint64) uint64
	}{
		"unset reports zero": {
			want: func(uint64) uint64 { return 0 },
		},
		"pdi follows the derived PDI": {
			guid: &FabricGUIDConfig{FromPDI: true},
			want: func(derived uint64) uint64 { return derived },
		},
		"pdi follows a configured PDI": {
			pdi:  &configured,
			guid: &FabricGUIDConfig{FromPDI: true},
			want: func(uint64) uint64 { return uint64(configured) },
		},
		"fixed value ignores the PDI": {
			guid: &FabricGUIDConfig{Value: 0xab},
			want: func(uint64) uint64 { return 0xab },
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			derived, ret := makePlatformDevice(t, &DeviceConfig{}).GetPdi()
			require.Equal(t, nvml.SUCCESS, ret)

			dev := makePlatformDevice(t, &DeviceConfig{
				PDI:      tc.pdi,
				Platform: &PlatformConfig{ModuleID: 1, GPUFabricGUID: tc.guid},
			})
			info, ret := dev.GetMockPlatformInfo()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tc.want(derived.Value), info.FabricGUID)
		})
	}
}
