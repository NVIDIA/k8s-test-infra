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
	if err != nil {
		t.Fatalf("parseNVMLPrototypes: %v", err)
	}

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
			if !ok {
				t.Fatalf("function %s not found in parsed prototypes", tt.name)
			}
			if len(proto.Params) != len(tt.wantParams) {
				t.Fatalf("param count: got %d, want %d\n  got:  %+v\n  want: %+v",
					len(proto.Params), len(tt.wantParams), proto.Params, tt.wantParams)
			}
			for i, want := range tt.wantParams {
				got := proto.Params[i]
				if got.CType != want.CType || got.Name != want.Name {
					t.Errorf("param[%d]: got {%q, %q}, want {%q, %q}",
						i, got.CType, got.Name, want.CType, want.Name)
				}
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
			if got != tt.goType {
				t.Errorf("cTypeToGo(%q) = %q, want %q", tt.cType, got, tt.goType)
			}
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
	if !strings.Contains(stub, "//export nvmlDeviceGetTemperature") {
		t.Error("missing //export directive")
	}
	// Should contain correct Go function signature
	if !strings.Contains(stub, "func nvmlDeviceGetTemperature(device C.nvmlDevice_t, sensorType C.nvmlTemperatureSensors_t, temp *C.uint)") {
		t.Errorf("incorrect function signature in:\n%s", stub)
	}
	// Should return stubReturn
	if !strings.Contains(stub, `return stubReturn("nvmlDeviceGetTemperature")`) {
		t.Error("missing stubReturn call")
	}
}

func TestGenerateStubWithSignatureVoid(t *testing.T) {
	proto := FuncProto{
		Name:   "nvmlInit_v2",
		Params: nil,
	}

	stub := generateStubWithSignature(proto)

	if !strings.Contains(stub, "func nvmlInit_v2() C.nvmlReturn_t") {
		t.Errorf("incorrect void function signature in:\n%s", stub)
	}
}

func TestGenerateStubWithSignaturePointerParam(t *testing.T) {
	proto := FuncProto{
		Name: "nvmlDeviceGetCount_v2",
		Params: []CParam{
			{CType: "unsigned int *", Name: "deviceCount"},
		},
	}

	stub := generateStubWithSignature(proto)

	if !strings.Contains(stub, "func nvmlDeviceGetCount_v2(deviceCount *C.uint)") {
		t.Errorf("incorrect pointer param signature in:\n%s", stub)
	}
}

func TestScanBridgeExports(t *testing.T) {
	dir := t.TempDir()

	deviceGo := `package main

//export nvmlDeviceGetCount_v2
func nvmlDeviceGetCount_v2() {}

//export nvmlDeviceGetName
func nvmlDeviceGetName() {}
`
	if err := os.WriteFile(filepath.Join(dir, "device.go"), []byte(deviceGo), 0644); err != nil {
		t.Fatal(err)
	}

	initGo := `package main

//export nvmlInit_v2
func nvmlInit_v2() {}
`
	if err := os.WriteFile(filepath.Join(dir, "init.go"), []byte(initGo), 0644); err != nil {
		t.Fatal(err)
	}

	// stubs_generated.go should be SKIPPED
	stubsGo := `package main

//export nvmlStubFunction
func nvmlStubFunction() {}
`
	if err := os.WriteFile(filepath.Join(dir, "stubs_generated.go"), []byte(stubsGo), 0644); err != nil {
		t.Fatal(err)
	}

	exports, err := scanBridgeExports(dir)
	if err != nil {
		t.Fatalf("scanBridgeExports: %v", err)
	}

	if len(exports) != 3 {
		t.Fatalf("expected 3 exports, got %d: %v", len(exports), exports)
	}

	for _, fn := range []string{"nvmlDeviceGetCount_v2", "nvmlDeviceGetName", "nvmlInit_v2"} {
		if !exports[fn] {
			t.Errorf("expected export %q not found", fn)
		}
	}

	if exports["nvmlStubFunction"] {
		t.Error("stubs_generated.go should be skipped but nvmlStubFunction was found")
	}
}

func TestScanBridgeExportsEmptyDir(t *testing.T) {
	dir := t.TempDir()

	exports, err := scanBridgeExports(dir)
	if err != nil {
		t.Fatalf("scanBridgeExports: %v", err)
	}

	if len(exports) != 0 {
		t.Fatalf("expected 0 exports from empty dir, got %d", len(exports))
	}
}
