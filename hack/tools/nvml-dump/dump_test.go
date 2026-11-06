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

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// fakeDevice has the method shapes go-nvml's Device has: a value and a Return,
// a bare Return, several values, and an argument that makes a getter skipped.
type fakeDevice struct{}

func (fakeDevice) GetName() (string, nvml.Return)          { return "Board", nvml.SUCCESS }
func (fakeDevice) GetBrand() (nvml.BrandType, nvml.Return) { return 7, nvml.SUCCESS }
func (fakeDevice) GetMigMode() (int, int, nvml.Return)     { return 0, 1, nvml.SUCCESS }
func (fakeDevice) GetSerial() (string, nvml.Return)        { return "", nvml.ERROR_NOT_SUPPORTED }
func (fakeDevice) IsFabricAttached() nvml.Return           { return nvml.ERROR_NOT_SUPPORTED }
func (fakeDevice) GetTemperature(sensor int) (uint32, nvml.Return) {
	return uint32(sensor), nvml.SUCCESS
}
func (fakeDevice) GetClockInfo(kind nvml.ClockType) (uint32, nvml.Return) {
	if kind > nvml.CLOCK_MEM {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	return 1000 + uint32(kind), nvml.SUCCESS
}
func (fakeDevice) GetNvLinkState(link int) (nvml.EnableState, nvml.Return) {
	if link >= 3 {
		return 0, nvml.ERROR_INVALID_ARGUMENT
	}
	return 1, nvml.SUCCESS
}
func (fakeDevice) GetMemoryLimits(_ string) (int, nvml.Return) {
	panic("string argument must be skipped")
}
func (fakeDevice) GetAccountingStats(pid uint32) (int, nvml.Return) { return int(pid), nvml.SUCCESS }
func (fakeDevice) GetNeedsStruct(_ *nvml.PciInfo) nvml.Return {
	panic("pointer argument must be skipped")
}
func (fakeDevice) GetFieldValues(values []nvml.FieldValue) nvml.Return {
	for i := range values {
		if values[i].FieldId == 7 {
			values[i].NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
			continue
		}
		values[i].ValueType = 4
		values[i].Value[0] = byte(values[i].FieldId)
	}
	return nvml.SUCCESS
}
func (fakeDevice) GetPanicky() (string, nvml.Return) { panic("symbol missing") }
func (fakeDevice) SetPersistenceMode(int) nvml.Return {
	panic("a setter must never be called")
}

func TestCallGettersRecordsReturnCodesAndValues(t *testing.T) {
	calls, _ := callGetters(fakeDevice{})

	require.Equal(t, call{Ret: "SUCCESS", Value: "Board"}, calls["GetName"])
	require.Equal(t, "ERROR_NOT_SUPPORTED", calls["GetSerial"].Ret)
	require.Equal(t, "ERROR_NOT_SUPPORTED", calls["IsFabricAttached"].Ret)
	require.Equal(t, []any{0, 1}, calls["GetMigMode"].Value)
}

func TestCallGettersSkipsArgumentsAndNeverCallsSetters(t *testing.T) {
	calls, skipped := callGetters(fakeDevice{})
	_ = calls

	require.ElementsMatch(t, []string{"GetMemoryLimits", "GetNeedsStruct"}, skipped)
	require.NotContains(t, calls, "SetPersistenceMode")
}

func TestCallGettersSurvivesAPanickingGetter(t *testing.T) {
	calls, _ := callGetters(fakeDevice{})

	require.Contains(t, calls["GetPanicky"].Ret, "PANIC")
	require.Equal(t, "SUCCESS", calls["GetName"].Ret)
}

func dumpOf(calls map[string]call) dump {
	return dump{Devices: []deviceDump{{Index: 0, Calls: calls}}}
}

func TestDiffClassifiesEveryDifference(t *testing.T) {
	hw := dumpOf(map[string]call{
		"GetName":        {Ret: "SUCCESS", Value: "Board"},
		"GetBrand":       {Ret: "SUCCESS", Value: 7},
		"GetSerial":      {Ret: "ERROR_NOT_SUPPORTED"},
		"GetTemperature": {Ret: "SUCCESS", Value: 31},
		"GetOnlyReal":    {Ret: "SUCCESS", Value: 1},
	})
	mock := dumpOf(map[string]call{
		"GetName":        {Ret: "SUCCESS", Value: "Board"},
		"GetBrand":       {Ret: "SUCCESS", Value: 8},
		"GetSerial":      {Ret: "SUCCESS", Value: "x"},
		"GetTemperature": {Ret: "SUCCESS", Value: 90},
		"GetOnlyMock":    {Ret: "SUCCESS", Value: 1},
	})

	got := map[string]verdict{}
	for _, f := range compare(hw, mock, regexp.MustCompile(defaultIgnore)) {
		got[f.name] = f.verdict
	}

	require.Equal(t, match, got["GetName"])
	require.Equal(t, valueDiffers, got["GetBrand"])
	require.Equal(t, retDiffers, got["GetSerial"])
	require.Equal(t, ignoredValue, got["GetTemperature"], "a load-dependent reading is not compared")
	require.Equal(t, onlyReal, got["GetOnlyReal"])
	require.Equal(t, onlyMock, got["GetOnlyMock"])
}

func TestReportQuotesRatios(t *testing.T) {
	hw := dumpOf(map[string]call{"GetName": {Ret: "SUCCESS", Value: "A"}, "GetBrand": {Ret: "SUCCESS", Value: 1}})
	mock := dumpOf(map[string]call{"GetName": {Ret: "SUCCESS", Value: "A"}, "GetBrand": {Ret: "SUCCESS", Value: 2}})

	var out bytes.Buffer
	report(&out, compare(hw, mock, regexp.MustCompile(defaultIgnore)), 10)

	require.Contains(t, out.String(), "return code matches:   2 of 2 (100.0%)")
	require.Contains(t, out.String(), "value matches:         1 of 2 (50.0%)")
	require.Contains(t, out.String(), "GetBrand")
}

func TestRetNameDoesNotDependOnTheLoadedLibrary(t *testing.T) {
	require.Equal(t, "SUCCESS", retName(nvml.SUCCESS))
	require.Equal(t, "ERROR_NOT_SUPPORTED", retName(nvml.ERROR_NOT_SUPPORTED))
	require.Equal(t, "ERROR_77", retName(nvml.Return(77)))
}

func TestCallGettersEnumeratesIntegerAndEnumArguments(t *testing.T) {
	calls, _ := callGetters(fakeDevice{})

	// A plain int index is tried at 0 and 1.
	require.Equal(t, call{Ret: "SUCCESS", Value: uint32(0)}, calls["GetTemperature(0)"])
	require.Equal(t, call{Ret: "SUCCESS", Value: uint32(1)}, calls["GetTemperature(1)"])

	// A clock type is tried over 0..3, and values past the end are recorded.
	require.Equal(t, call{Ret: "SUCCESS", Value: uint32(1000)}, calls["GetClockInfo(0)"])
	require.Equal(t, "ERROR_INVALID_ARGUMENT", calls["GetClockInfo(3)"].Ret)

	// NVLink indexes run past any real link count, so the end shows up.
	require.Equal(t, "SUCCESS", calls["GetNvLinkState(2)"].Ret)
	require.Equal(t, "ERROR_INVALID_ARGUMENT", calls["GetNvLinkState(3)"].Ret)
	require.Contains(t, calls, "GetNvLinkState(39)")

	require.Contains(t, calls, "GetAccountingStats(0)")
	require.Contains(t, calls, "GetAccountingStats(1)")
}

func TestCallGettersProbesEveryFieldID(t *testing.T) {
	calls, _ := callGetters(fakeDevice{})

	require.Len(t, filterPrefix(calls, "GetFieldValues("), fieldIDLimit)
	require.Equal(t, "SUCCESS", calls["GetFieldValues(id=3)"].Ret)
	require.Equal(t, "ERROR_NOT_SUPPORTED", calls["GetFieldValues(id=7)"].Ret,
		"the per-field return code is what is recorded")
	require.Equal(t, map[string]any{"valueType": uint32(4), "value": "0300000000000000"},
		calls["GetFieldValues(id=3)"].Value)
}

func filterPrefix(calls map[string]call, prefix string) []string {
	var out []string
	for name := range calls {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			out = append(out, name)
		}
	}
	return out
}

func TestLoadReadsGzippedDumps(t *testing.T) {
	raw := []byte(`{"devices":[{"index":0,"calls":{"GetName":{"ret":"SUCCESS","value":"Board"}}}]}`)
	dir := t.TempDir()
	plain := filepath.Join(dir, "d.json")
	require.NoError(t, os.WriteFile(plain, raw, 0o644))

	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	gz := filepath.Join(dir, "d.json.gz")
	require.NoError(t, os.WriteFile(gz, zipped.Bytes(), 0o644))

	want := dump{Devices: []deviceDump{{Index: 0, Calls: map[string]call{"GetName": {Ret: "SUCCESS", Value: "Board"}}}}}
	for _, path := range []string{plain, gz} {
		got, err := load(path)
		require.NoError(t, err)
		require.Equal(t, want, got, path)
	}
}

func TestEmitterWritesValidRedactedJSONOneDeviceAtATime(t *testing.T) {
	var buf bytes.Buffer
	var errb bytes.Buffer
	e := newEmitter(&buf)
	e.begin(map[string]call{"DeviceGetCount": {Ret: "SUCCESS", Value: 2}})
	pdi := map[string]any{"Version": 1, "Value": uint64(16078824392434643224)}
	e.device(deviceDump{Index: 0, Calls: map[string]call{"GetPdi": {Ret: "SUCCESS", Value: pdi}}})
	require.Contains(t, buf.String(), `"index":0`, "a finished device is already written")
	e.device(deviceDump{Index: 1, Calls: map[string]call{"GetPdi": {Ret: "SUCCESS", Value: pdi}}})
	require.Equal(t, 0, e.end([]string{"GetNeedsStruct"}, 0, &errb))

	require.NotContains(t, buf.String(), "16078824392434643224", "the PDI is redacted at the source")
	var d dump
	require.NoError(t, json.Unmarshal(buf.Bytes(), &d))
	require.Len(t, d.Devices, 2)
	require.Equal(t, []string{"GetNeedsStruct"}, d.Skipped)
}

func TestValueMatchesCountOnlyCallsThatSucceededOnBothSides(t *testing.T) {
	hw := dumpOf(map[string]call{
		"GetBoardId":      {Ret: "ERROR_NOT_SUPPORTED"},
		"GetMaxClockInfo": {Ret: "SUCCESS", Value: 2000},
		"GetClockInfo":    {Ret: "SUCCESS", Value: 1000},
		"GetNumGpuCores":  {Ret: "SUCCESS", Value: 100},
	})
	mock := dumpOf(map[string]call{
		"GetBoardId":      {Ret: "ERROR_NOT_SUPPORTED"},
		"GetMaxClockInfo": {Ret: "SUCCESS", Value: 1500},
		"GetClockInfo":    {Ret: "SUCCESS", Value: 1},
		"GetNumGpuCores":  {Ret: "SUCCESS", Value: 100},
	})
	got := map[string]verdict{}
	for _, f := range compare(hw, mock, regexp.MustCompile(defaultIgnore)) {
		got[f.name] = f.verdict
	}

	require.Equal(t, sameError, got["GetBoardId"], "the same error on both sides is not a value match")
	require.Equal(t, valueDiffers, got["GetMaxClockInfo"], "a board value is not hidden by the GetClockInfo rule")
	require.Equal(t, ignoredValue, got["GetClockInfo"], "a reading that moves with load is not compared")
	require.Equal(t, match, got["GetNumGpuCores"])

	var out bytes.Buffer
	report(&out, compare(hw, mock, regexp.MustCompile(defaultIgnore)), 10)
	require.Contains(t, out.String(), "value matches:         1 of 2 (50.0%)")
	require.Contains(t, out.String(), "return code matches:   4 of 4")
}

func TestDevicesOnlyOneSideHasCountAsMissing(t *testing.T) {
	hw := dump{Devices: []deviceDump{
		{Index: 0, Calls: map[string]call{"GetName": {Ret: "SUCCESS", Value: "Board"}}},
		{Index: 1, Calls: map[string]call{"GetName": {Ret: "SUCCESS", Value: "Board"}}},
	}}
	mock := dump{Devices: []deviceDump{
		{Index: 0, Calls: map[string]call{"GetName": {Ret: "SUCCESS", Value: "Board"}}},
	}}

	got := map[verdict]int{}
	for _, f := range compare(hw, mock, regexp.MustCompile(defaultIgnore)) {
		got[f.verdict]++
	}
	require.Equal(t, map[verdict]int{match: 1, onlyReal: 1}, got)

	got = map[verdict]int{}
	for _, f := range compare(mock, hw, regexp.MustCompile(defaultIgnore)) {
		got[f.verdict]++
	}
	require.Equal(t, map[verdict]int{match: 1, onlyMock: 1}, got)
}

func TestLoadKeepsLargeIntegersExact(t *testing.T) {
	dir := t.TempDir()
	write := func(name, mask string) string {
		p := filepath.Join(dir, name)
		raw := `{"devices":[{"index":0,"calls":{"GetCpuAffinity":{"ret":"SUCCESS","value":[` + mask + `]}}}]}`
		require.NoError(t, os.WriteFile(p, []byte(raw), 0o644))
		return p
	}
	a, err := load(write("a.json", "9223372036854775808"))
	require.NoError(t, err)
	b, err := load(write("b.json", "9223372036854775809"))
	require.NoError(t, err)

	findings := compare(a, b, regexp.MustCompile(defaultIgnore))
	require.Len(t, findings, 1)
	require.Equal(t, valueDiffers, findings[0].verdict)
}
