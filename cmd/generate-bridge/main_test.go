// Copyright (c) 2025-2026, NVIDIA CORPORATION.  All rights reserved.
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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNVMLPrototypes(t *testing.T) {
	header := `
nvmlReturn_t DECLDIR nvmlInit_v2(void);
nvmlReturn_t DECLDIR nvmlShutdown(void);
nvmlReturn_t DECLDIR nvmlDeviceGetTemperature(nvmlDevice_t device, nvmlTemperatureSensors_t sensorType, unsigned int *temp);
nvmlReturn_t DECLDIR nvmlDeviceGetCount_v2(unsigned int *deviceCount);
nvmlReturn_t DECLDIR nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t *device);
nvmlReturn_t DECLDIR nvmlDeviceGetName(nvmlDevice_t device, char *name, unsigned int length);
nvmlReturn_t DECLDIR nvmlDeviceCreateGpuInstance(nvmlDevice_t device, unsigned int profileId,
                                                 nvmlGpuInstance_t *gpuInstance);
DEPRECATED(13.0) nvmlReturn_t DECLDIR nvmlDeviceGetHandleBySerial(const char *serial, nvmlDevice_t *device);
`
	protos, err := parseNVMLPrototypes(strings.NewReader(header))
	require.NoError(t, err, "parseNVMLPrototypes")

	tests := []struct {
		name       string
		wantParams []CParam
	}{
		{
			name:       "nvmlInit_v2",
			wantParams: nil, // void params
		},
		{
			name:       "nvmlShutdown",
			wantParams: nil,
		},
		{
			name: "nvmlDeviceGetTemperature",
			wantParams: []CParam{
				{CType: "nvmlDevice_t", Name: "device"},
				{CType: "nvmlTemperatureSensors_t", Name: "sensorType"},
				{CType: "unsigned int *", Name: "temp"},
			},
		},
		{
			name: "nvmlDeviceGetCount_v2",
			wantParams: []CParam{
				{CType: "unsigned int *", Name: "deviceCount"},
			},
		},
		{
			name: "nvmlDeviceGetHandleByIndex_v2",
			wantParams: []CParam{
				{CType: "unsigned int", Name: "index"},
				{CType: "nvmlDevice_t *", Name: "device"},
			},
		},
		{
			name: "nvmlDeviceGetName",
			wantParams: []CParam{
				{CType: "nvmlDevice_t", Name: "device"},
				{CType: "char *", Name: "name"},
				{CType: "unsigned int", Name: "length"},
			},
		},
		{
			name: "nvmlDeviceCreateGpuInstance",
			wantParams: []CParam{
				{CType: "nvmlDevice_t", Name: "device"},
				{CType: "unsigned int", Name: "profileId"},
				{CType: "nvmlGpuInstance_t *", Name: "gpuInstance"},
			},
		},
		{
			name: "nvmlDeviceGetHandleBySerial",
			wantParams: []CParam{
				{CType: "const char *", Name: "serial"},
				{CType: "nvmlDevice_t *", Name: "device"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto, ok := protos[tt.name]
			require.True(t, ok, "function %s not found in parsed prototypes", tt.name)
			require.Len(t, proto.Params, len(tt.wantParams),
				"param count\n  got:  %+v\n  want: %+v", proto.Params, tt.wantParams)
			for i, want := range tt.wantParams {
				got := proto.Params[i]
				require.Equal(t, want, got, "param[%d]", i)
			}
		})
	}
}

func TestCTypeToGo(t *testing.T) {
	tests := []struct {
		cType  string
		goType string
	}{
		{"nvmlDevice_t", "C.nvmlDevice_t"},
		{"nvmlDevice_t *", "*C.nvmlDevice_t"},
		{"unsigned int", "C.uint"},
		{"unsigned int *", "*C.uint"},
		{"unsigned long long", "C.ulonglong"},
		{"unsigned long long *", "*C.ulonglong"},
		{"unsigned long *", "*C.ulong"},
		{"int", "C.int"},
		{"int *", "*C.int"},
		{"char *", "*C.char"},
		{"char*", "*C.char"},
		{"const char *", "*C.char"},
		{"nvmlTemperatureSensors_t", "C.nvmlTemperatureSensors_t"},
		{"nvmlBrandType_t *", "*C.nvmlBrandType_t"},
		{"nvmlMemory_t *", "*C.nvmlMemory_t"},
		{"nvmlEnableState_t", "C.nvmlEnableState_t"},
		{"nvmlEnableState_t *", "*C.nvmlEnableState_t"},
		{"nvmlPciInfo_t *", "*C.nvmlPciInfo_t"},
	}

	for _, tt := range tests {
		t.Run(tt.cType, func(t *testing.T) {
			got := cTypeToGo(tt.cType)
			require.Equal(t, tt.goType, got, "cTypeToGo(%q)", tt.cType)
		})
	}
}

func TestGenerateStubWithSignature(t *testing.T) {
	proto := FuncProto{
		Name: "nvmlDeviceGetTemperature",
		Params: []CParam{
			{CType: "nvmlDevice_t", Name: "device"},
			{CType: "nvmlTemperatureSensors_t", Name: "sensorType"},
			{CType: "unsigned int *", Name: "temp"},
		},
	}

	stub := generateStubWithSignature(proto)

	// Should contain //export directive
	require.Contains(t, stub, "//export nvmlDeviceGetTemperature", "missing //export directive")
	// Should contain correct Go function signature
	require.Contains(t, stub, "func nvmlDeviceGetTemperature(device C.nvmlDevice_t, sensorType C.nvmlTemperatureSensors_t, temp *C.uint)", "incorrect function signature in:\n%s", stub)
	// Should return stubReturn
	require.Contains(t, stub, `return stubReturn("nvmlDeviceGetTemperature")`, "missing stubReturn call")
}

func TestGenerateStubWithSignatureVoid(t *testing.T) {
	proto := FuncProto{
		Name:   "nvmlInit_v2",
		Params: nil,
	}

	stub := generateStubWithSignature(proto)

	require.Contains(t, stub, "func nvmlInit_v2() C.nvmlReturn_t", "incorrect void function signature in:\n%s", stub)
}

func TestGenerateStubWithSignaturePointerParam(t *testing.T) {
	proto := FuncProto{
		Name: "nvmlDeviceGetCount_v2",
		Params: []CParam{
			{CType: "unsigned int *", Name: "deviceCount"},
		},
	}

	stub := generateStubWithSignature(proto)

	require.Contains(t, stub, "func nvmlDeviceGetCount_v2(deviceCount *C.uint)", "incorrect pointer param signature in:\n%s", stub)
}

func TestScanBridgeExports(t *testing.T) {
	dir := t.TempDir()

	deviceGo := `package main

//export nvmlDeviceGetCount_v2
func nvmlDeviceGetCount_v2() {}

//export nvmlDeviceGetName
func nvmlDeviceGetName() {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "device.go"), []byte(deviceGo), 0644))

	initGo := `package main

//export nvmlInit_v2
func nvmlInit_v2() {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.go"), []byte(initGo), 0644))

	// stubs_generated.go should be SKIPPED
	stubsGo := `package main

//export nvmlStubFunction
func nvmlStubFunction() {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stubs_generated.go"), []byte(stubsGo), 0644))

	exports, err := scanBridgeExports(dir)
	require.NoError(t, err, "scanBridgeExports")

	require.Len(t, exports, 3, "exports: %v", exports)

	for _, fn := range []string{"nvmlDeviceGetCount_v2", "nvmlDeviceGetName", "nvmlInit_v2"} {
		require.True(t, exports[fn], "expected export %q not found", fn)
	}

	require.False(t, exports["nvmlStubFunction"], "stubs_generated.go should be skipped but nvmlStubFunction was found")
}

func TestScanBridgeExportsEmptyDir(t *testing.T) {
	dir := t.TempDir()

	exports, err := scanBridgeExports(dir)
	require.NoError(t, err, "scanBridgeExports")

	require.Empty(t, exports, "expected 0 exports from empty dir")
}

func TestFindMissing(t *testing.T) {
	all := []string{"nvmlInit_v2", "nvmlShutdown", "nvmlDeviceGetCount_v2", "nvmlDeviceGetName"}
	existing := map[string]bool{
		"nvmlInit_v2":  true,
		"nvmlShutdown": true,
	}

	missing := findMissing(all, existing)

	require.Len(t, missing, 2, "missing: %v", missing)
	require.Equal(t, "nvmlDeviceGetCount_v2", missing[0], "unexpected missing functions: %v", missing)
	require.Equal(t, "nvmlDeviceGetName", missing[1], "unexpected missing functions: %v", missing)
}

func TestFindMissingNoneImplemented(t *testing.T) {
	all := []string{"a", "b", "c"}
	existing := map[string]bool{}

	missing := findMissing(all, existing)
	require.Len(t, missing, 3)
}

func TestFindMissingAllImplemented(t *testing.T) {
	all := []string{"a", "b"}
	existing := map[string]bool{"a": true, "b": true}

	missing := findMissing(all, existing)
	require.Empty(t, missing)
}

func TestLookupProtoDirectMatch(t *testing.T) {
	protos := map[string]FuncProto{
		"nvmlInit_v2": {Name: "nvmlInit_v2", Params: nil},
	}

	proto, ok := lookupProto("nvmlInit_v2", protos)
	require.True(t, ok, "expected direct match")
	require.Equal(t, "nvmlInit_v2", proto.Name)
}

func TestLookupProtoVersionFallback(t *testing.T) {
	protos := map[string]FuncProto{
		"nvmlDeviceGetCount": {
			Name:   "nvmlDeviceGetCount",
			Params: []CParam{{CType: "unsigned int *", Name: "deviceCount"}},
		},
	}

	proto, ok := lookupProto("nvmlDeviceGetCount_v2", protos)
	require.True(t, ok, "expected fallback match for _v2 → unversioned")
	require.Len(t, proto.Params, 1, "expected 1 param")
}

func TestLookupProtoNoMatch(t *testing.T) {
	protos := map[string]FuncProto{}

	_, ok := lookupProto("nvmlNonexistent", protos)
	require.False(t, ok, "expected no match for nonexistent function")
}

func TestPrintStats(t *testing.T) {
	dir := t.TempDir()

	deviceGo := `package main

//export nvmlDeviceGetCount_v2
func nvmlDeviceGetCount_v2() {}

//export nvmlDeviceGetName
func nvmlDeviceGetName() {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "device.go"), []byte(deviceGo), 0644))

	initGo := `package main

//export nvmlInit_v2
func nvmlInit_v2() {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.go"), []byte(initGo), 0644))

	allFunctions := []string{"nvmlDeviceGetCount_v2", "nvmlDeviceGetName", "nvmlInit_v2", "nvmlShutdown", "nvmlFoo"}

	var buf strings.Builder
	printStats(&buf, allFunctions, dir)
	output := buf.String()

	require.Contains(t, output, "Total functions", "expected 'Total functions' in output:\n%s", output)
	require.Contains(t, output, "5", "expected total count 5 in output:\n%s", output)
	require.Contains(t, output, "device.go", "expected 'device.go' in per-file breakdown:\n%s", output)
}

func TestValidateSignatures(t *testing.T) {
	dir := t.TempDir()

	// Correct: 1 param matching prototype
	correctGo := `package main

//export nvmlDeviceGetCount_v2
func nvmlDeviceGetCount_v2(deviceCount *C.uint) C.nvmlReturn_t {
	return 0
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "correct.go"), []byte(correctGo), 0644))

	// Wrong: 0 params but prototype says 3
	wrongGo := `package main

//export nvmlDeviceGetName
func nvmlDeviceGetName() C.nvmlReturn_t {
	return 0
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wrong.go"), []byte(wrongGo), 0644))

	protos := map[string]FuncProto{
		"nvmlDeviceGetCount_v2": {
			Name:   "nvmlDeviceGetCount_v2",
			Params: []CParam{{CType: "unsigned int *", Name: "deviceCount"}},
		},
		"nvmlDeviceGetName": {
			Name: "nvmlDeviceGetName",
			Params: []CParam{
				{CType: "nvmlDevice_t", Name: "device"},
				{CType: "char *", Name: "name"},
				{CType: "unsigned int", Name: "length"},
			},
		},
	}

	mismatches, err := validateSignatures(dir, protos)
	require.NoError(t, err, "validateSignatures")

	require.Len(t, mismatches, 1, "mismatches: %v", mismatches)
	require.Contains(t, mismatches[0], "nvmlDeviceGetName", "expected mismatch for nvmlDeviceGetName, got: %s", mismatches[0])
}
