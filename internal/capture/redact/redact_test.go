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

package redact

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// Values chosen to be recognisable if one survives.
var (
	clusterUUID = [16]uint8{0xa2, 0xd9, 0xad, 0xa9, 0x24, 0x97, 0x42, 0x31, 0x9b, 0xcd, 0x5a, 0x8f, 0xa2, 0xc1, 0xd4, 0xd6}
	ibGUID      = [16]uint8{0x74, 0x25, 0x54, 0x03, 0x00, 0xd4, 0xdd, 0x09}
	chassis     = [16]uint8{'1', '8', '2', '1', '5', '2', '6', 'A', '7', '4', '0', '0', '3'}
	rackGUID    = [16]uint8{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	pdiValue    = uint64(16078824392434643224)
)

// dumpOf builds a document in the NVML dump's layout from go-nvml's own structures,
// so the field names under test are the ones the library really has.
func dumpOf(t *testing.T, calls map[string]any) []byte {
	t.Helper()
	entries := map[string]any{}
	for name, v := range calls {
		entries[name] = map[string]any{"ret": "SUCCESS", "value": v}
	}
	raw, err := json.Marshal(map[string]any{
		"system":  map[string]any{},
		"devices": []any{map[string]any{"index": 0, "calls": entries}, map[string]any{"index": 1, "calls": entries}},
	})
	require.NoError(t, err)
	return raw
}

func realCalls() map[string]any {
	return map[string]any{
		"GetPdi":              nvml.Pdi{Version: 16777232, Value: pdiValue},
		"GetPlatformInfo":     nvml.PlatformInfo{Version: 16777260, IbGuid: ibGUID, ChassisSerialNumber: chassis, SlotNumber: 4, TrayIndex: 3, HostId: 1},
		"GetPlatformInfo_v1":  nvml.PlatformInfo_v1{Version: 1, IbGuid: ibGUID, RackGuid: rackGUID},
		"GetGpuFabricInfo":    nvml.GpuFabricInfo{ClusterUuid: clusterUUID, CliqueId: 65534, State: 3},
		"GetGpuFabricInfo_v2": nvml.GpuFabricInfo_v2{Version: 2, ClusterUuid: clusterUUID, CliqueId: 65534},
		"GetSerial":           "1821526A74003",
		"GetUUID":             "GPU-f662ab51-7dd5-545c-be52-5aa9606316be",
		"GetBoardPartNumber":  "699-2G558-0201-TS5",
	}
}

func TestEveryIdentityFieldOfGoNVMLStructsIsRedacted(t *testing.T) {
	out, err := New().JSON(dumpOf(t, realCalls()))
	require.NoError(t, err)
	got := string(out)

	for name, original := range map[string]string{
		"PDI":                 strconv.FormatUint(pdiValue, 10),
		"IB GUID":             listOf(ibGUID[:]),
		"chassis serial":      listOf(chassis[:]),
		"rack GUID":           listOf(rackGUID[:]),
		"fabric cluster UUID": listOf(clusterUUID[:]),
		"serial":              "1821526A74003",
		"GPU UUID":            "f662ab51",
	} {
		require.NotContains(t, got, original, "%s survived redaction", name)
	}
	// Not identifiers: topology and model facts stay.
	for _, kept := range []string{`"SlotNumber":4`, `"TrayIndex":3`, `"CliqueId":65534`, `699-2G558-0201-TS5`} {
		require.Contains(t, got, kept)
	}
}

func listOf(b []uint8) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = strconv.Itoa(int(c))
	}
	return strings.Join(parts, ",")
}

func TestOnePlaceholderPerDistinctValueAcrossDevices(t *testing.T) {
	out, err := New().JSON(dumpOf(t, realCalls()))
	require.NoError(t, err)

	var doc struct {
		Devices []struct {
			Calls map[string]struct {
				Value any `json:"value"`
			} `json:"calls"`
		} `json:"devices"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	field := func(dev int, call, name string) any {
		return doc.Devices[dev].Calls[call].Value.(map[string]any)[name]
	}
	a := field(0, "GetGpuFabricInfo", "ClusterUuid")
	require.Equal(t, a, field(1, "GetGpuFabricInfo", "ClusterUuid"), "two GPUs in one cluster share the placeholder")
	require.Equal(t, a, field(0, "GetGpuFabricInfo_v2", "ClusterUuid"), "and so does another getter")
	// A chassis serial is text, and stays text.
	chassisOut := field(0, "GetPlatformInfo", "ChassisSerialNumber").([]any)
	text := make([]byte, 0, len(chassisOut))
	for _, c := range chassisOut {
		text = append(text, byte(c.(float64)))
	}
	require.Equal(t, "1820000000001", strings.TrimRight(string(text), "\x00"))
}

func TestRedactionIsIdempotentAndKeepsAbsentValues(t *testing.T) {
	calls := realCalls()
	calls["GetGpuFabricInfo_zero"] = nvml.GpuFabricInfo{} // not in a fabric: all zeros
	r := New()
	first, err := r.JSON(dumpOf(t, calls))
	require.NoError(t, err)
	for _, want := range []string{
		`"GetUUID":{"ret":"SUCCESS","value":"GPU-00000000-0000-0000-0000-000000000000"}`,
		`"GetSerial":{"ret":"SUCCESS","value":"1650000000001"}`,
		`"ClusterUuid":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1]`,
		`"ClusterUuid":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]`, // absent stays absent
	} {
		require.Contains(t, string(first), want)
	}
	second, err := New().JSON(first)
	require.NoError(t, err)
	require.JSONEq(t, string(first), string(second), "redacting the output again changes nothing")
}

func TestNumbersPastTwoToTheFiftyThreeAreKeptExactly(t *testing.T) {
	raw := []byte(`{"devices":[{"index":0,"calls":{"GetOther":{"ret":"SUCCESS","value":{"Counter":18446744073709551615}}}}]}`)
	out, err := New().JSON(raw)
	require.NoError(t, err)
	require.Contains(t, string(out), "18446744073709551615")
}

// twoGPUs is a dump of two GPUs with different UUIDs in one fabric cluster.
func twoGPUs(t *testing.T) []byte {
	t.Helper()
	dev := func(i int, uuid string) map[string]any {
		return map[string]any{"index": i, "calls": map[string]any{
			"GetUUID":          map[string]any{"ret": "SUCCESS", "value": uuid},
			"GetGpuFabricInfo": map[string]any{"ret": "SUCCESS", "value": nvml.GpuFabricInfo{ClusterUuid: clusterUUID}},
			"GetPlatformInfo":  map[string]any{"ret": "SUCCESS", "value": nvml.PlatformInfo{IbGuid: ibGUID, ChassisSerialNumber: chassis}},
		}}
	}
	raw, err := json.Marshal(map[string]any{"system": map[string]any{}, "devices": []any{
		dev(0, "GPU-f662ab51-7dd5-545c-be52-5aa9606316be"),
		dev(1, "GPU-e88c50aa-7e38-5b26-8005-fabdba9d7fec"),
	}})
	require.NoError(t, err)
	return raw
}

func TestPlaceholdersAreTheSameOnEveryRun(t *testing.T) {
	raw := twoGPUs(t)
	first, err := New().JSON(raw)
	require.NoError(t, err)
	for _, want := range []string{
		`"value":"GPU-00000000-0000-0000-0000-000000000000"`,
		`"value":"GPU-00000001-0000-0000-0000-000000000001"`,
		`"ClusterUuid":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1]`,
		`"IbGuid":[0,0,0,0,0,0,0,1,0,0,0,0,0,0,0,0]`,
	} {
		require.Contains(t, string(first), want)
	}
	for i := 0; i < 50; i++ {
		again, err := New().JSON(raw)
		require.NoError(t, err)
		require.Equal(t, string(first), string(again), "run %d differs: map order leaked into the numbering", i)
	}
}

func TestGPUUUIDsCountFromZeroInDeviceOrderApartFromTheCluster(t *testing.T) {
	out, err := New().JSON(twoGPUs(t))
	require.NoError(t, err)
	got := string(out)

	require.Contains(t, got, "GPU-00000000-0000-0000-0000-000000000000", "GPU 0")
	require.Contains(t, got, "GPU-00000001-0000-0000-0000-000000000001", "GPU 1")
	require.NotContains(t, got, "GPU-00000002", "the fabric cluster UUID does not use the GPU counter")
	require.Contains(t, got, `"ClusterUuid":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,1]`, "the cluster counts from 1 on its own")
}
