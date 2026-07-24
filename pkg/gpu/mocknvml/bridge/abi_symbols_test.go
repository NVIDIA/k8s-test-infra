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
	"os"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
	"github.com/stretchr/testify/require"
)

func TestNVML13SymbolsAreExportedAndVersioned(t *testing.T) {
	stubs, err := os.ReadFile("stubs_generated.go")
	require.NoError(t, err)
	types, err := os.ReadFile("nvml_types.h")
	require.NoError(t, err)

	requiredSymbols := []struct {
		name              string
		addedDriver       string
		addedNVMLLibrary  string
		requiredOpaqueTyp string
	}{
		{
			name:              "nvmlDeviceGetVgpuSchedulerLog_v2",
			addedDriver:       "580.0",
			addedNVMLLibrary:  "13.0",
			requiredOpaqueTyp: "nvmlVgpuSchedulerLogInfo_v2_t",
		},
		{
			name:              "nvmlDeviceGetVgpuSchedulerState_v2",
			addedDriver:       "580.0",
			addedNVMLLibrary:  "13.0",
			requiredOpaqueTyp: "nvmlVgpuSchedulerStateInfo_v2_t",
		},
		{
			name:              "nvmlDeviceSetVgpuSchedulerState_v2",
			addedDriver:       "580.0",
			addedNVMLLibrary:  "13.0",
			requiredOpaqueTyp: "nvmlVgpuSchedulerSetState_v2_t",
		},
		{
			name:             "nvmlDeviceVgpuForceGspUnload",
			addedDriver:      "580.0",
			addedNVMLLibrary: "13.0",
		},
		{
			name:              "nvmlGpuInstanceGetVgpuSchedulerLog_v2",
			addedDriver:       "580.0",
			addedNVMLLibrary:  "13.0",
			requiredOpaqueTyp: "nvmlVgpuSchedulerLogInfo_v2_t",
		},
		{
			name:              "nvmlGpuInstanceGetVgpuSchedulerState_v2",
			addedDriver:       "580.0",
			addedNVMLLibrary:  "13.0",
			requiredOpaqueTyp: "nvmlVgpuSchedulerStateInfo_v2_t",
		},
		{
			name:              "nvmlGpuInstanceSetVgpuSchedulerState_v2",
			addedDriver:       "580.0",
			addedNVMLLibrary:  "13.0",
			requiredOpaqueTyp: "nvmlVgpuSchedulerState_v2_t",
		},
	}

	registry := engine.GetFunctionRegistry()
	for _, symbol := range requiredSymbols {
		t.Run(symbol.name+"/nvml-"+symbol.addedNVMLLibrary, func(t *testing.T) {
			require.Contains(t, string(stubs), "//export "+symbol.name)

			if symbol.requiredOpaqueTyp != "" {
				require.True(t, strings.Contains(string(types), symbol.requiredOpaqueTyp),
					"missing opaque C type %s", symbol.requiredOpaqueTyp)
			}

			version, ok := registry[symbol.name]
			require.True(t, ok, "missing version registry entry for %s", symbol.name)
			require.Equal(t, symbol.addedDriver, version.Added)
		})
	}
}
