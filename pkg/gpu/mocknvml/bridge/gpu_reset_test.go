// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// SPDX-License-Identifier: Apache-2.0
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
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func TestResetGPU_UsesPhysicalIndexAfterFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	t.Setenv("MOCK_NVML_NUM_DEVICES", "2")
	t.Setenv("MOCK_NVML_CONFIG", "")
	t.Setenv("MOCK_NVML_OVERRIDES", path)
	engine.ResetForTesting()
	t.Cleanup(engine.ResetForTesting)
	e := engine.GetEngine()
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { require.Equal(t, nvml.SUCCESS, e.Shutdown()) })
	e.SetVisibleDevicesForTesting([]int{1})
	require.NoError(t, os.WriteFile(path, []byte(`version: 1
devices:
  "0":
    name: GPU-zero
  "1":
    name: GPU-one
`), 0600))
	h, ret := e.DeviceGetHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, 1, int(mockInternalResetGPU(h)))
	doc, err := mockctl.Load(path)
	require.NoError(t, err)
	require.Contains(t, doc.Devices, "0")
	require.NotContains(t, doc.Devices, "1")
}
