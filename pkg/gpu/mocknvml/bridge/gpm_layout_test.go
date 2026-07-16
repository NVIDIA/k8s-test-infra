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
	"testing"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

type goNvmlGpmSampleABI struct {
	Handle unsafe.Pointer
}

type goNvmlGpmMetricsGetABI struct {
	Version    uint32
	NumMetrics uint32
	Sample1    goNvmlGpmSampleABI
	Sample2    goNvmlGpmSampleABI
	Metrics    [333]nvml.GpmMetric
}

func TestGpmStructLayouts_MatchGoNvmlABI(t *testing.T) {
	require.Equal(t, uintptr(333), gpmMetricMaxForTest())

	require.Equal(t, unsafe.Sizeof(goNvmlGpmSampleABI{}), gpmSampleSizeForTest())
	require.Equal(t, unsafe.Offsetof(goNvmlGpmSampleABI{}.Handle), gpmSampleHandleOffsetForTest())

	require.Equal(t, unsafe.Sizeof(nvml.GpmMetric{}), gpmMetricSizeForTest())
	require.Equal(t, unsafe.Sizeof(goNvmlGpmMetricsGetABI{}), gpmMetricsGetSizeForTest())
	require.Equal(t, unsafe.Offsetof(goNvmlGpmMetricsGetABI{}.Sample1), gpmMetricsGetSample1OffsetForTest())
	require.Equal(t, unsafe.Offsetof(goNvmlGpmMetricsGetABI{}.Sample2), gpmMetricsGetSample2OffsetForTest())

	require.Equal(t, unsafe.Sizeof(nvml.GpmSupport{}), gpmSupportSizeForTest())
	require.Equal(t, uint32(nvml.GPM_SUPPORT_VERSION), gpmSupportVersionForTest())
}

func TestValidGpmSupportVersion(t *testing.T) {
	require.True(t, validGpmSupportVersion(nvml.GPM_SUPPORT_VERSION))
	require.False(t, validGpmSupportVersion(0))
	require.False(t, validGpmSupportVersion(nvml.GPM_SUPPORT_VERSION+1))
}
