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
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
)

func makeUtilizationDevice(t *testing.T, util *UtilizationConfig) *ConfigurableDevice {
	t.Helper()
	base := dgxa100.New()
	bd, _ := base.Devices[0].(*mockserver.Device)
	return NewConfigurableDevice(0, bd, &DeviceConfig{Utilization: util},
		"GPU-00000000-0000-0000-0000-000000000000", "0000:01:00.0", 0, (*NodeFabric)(nil))
}

// Issue #637: utilization.jpeg and utilization.ofa were parsed into the config
// and then dropped, so nvidia-smi reported N/A for both rows. The two values
// are deliberately distinct and non-zero here so neither a zeroed default nor a
// getter reading the wrong field can satisfy the assertions.
func TestConfigurableDevice_JpgAndOfaUtilizationReportConfiguredValues(t *testing.T) {
	dev := makeUtilizationDevice(t, &UtilizationConfig{Encoder: 7, Decoder: 9, JPEG: 35, OFA: 12})

	jpg, _, ret := dev.GetJpgUtilization()
	require.Equal(t, nvml.SUCCESS, ret, "GetJpgUtilization")
	require.Equal(t, uint32(35), jpg, "JPEG utilization must come from utilization.jpeg")

	ofa, _, ret := dev.GetOfaUtilization()
	require.Equal(t, nvml.SUCCESS, ret, "GetOfaUtilization")
	require.Equal(t, uint32(12), ofa, "OFA utilization must come from utilization.ofa")
}

// A config with no utilization block at all must still answer SUCCESS with a
// zero reading, matching how the encoder and decoder getters behave: NVML
// callers treat an error as "engine absent", which is a different claim than
// "engine idle".
func TestConfigurableDevice_JpgAndOfaUtilizationWithoutUtilizationBlock(t *testing.T) {
	dev := makeUtilizationDevice(t, nil)

	jpg, jpgPeriod, ret := dev.GetJpgUtilization()
	require.Equal(t, nvml.SUCCESS, ret, "GetJpgUtilization with no utilization block")
	require.Zero(t, jpg, "JPEG utilization")
	require.Zero(t, jpgPeriod, "JPEG sampling period follows the encoder/decoder convention")

	ofa, ofaPeriod, ret := dev.GetOfaUtilization()
	require.Equal(t, nvml.SUCCESS, ret, "GetOfaUtilization with no utilization block")
	require.Zero(t, ofa, "OFA utilization")
	require.Zero(t, ofaPeriod, "OFA sampling period follows the encoder/decoder convention")
}
