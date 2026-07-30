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
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
	mockserver "github.com/NVIDIA/go-nvml/pkg/nvml/mock/server"
	"github.com/stretchr/testify/require"
)

// Band literals transcribed from the `dynamic_metrics.utilization` block every
// profile ships. They are spelled out here rather than read back from the
// profile: an expectation derived from the same YAML the implementation reads
// would agree with any band, including one starting at 0, and would test
// nothing.
const (
	profileUtilGPUMin uint32 = 10
	profileUtilGPUMax uint32 = 45
	profileUtilMemMin uint32 = 5
	profileUtilMemMax uint32 = 25
)

// shippedProfiles is the full set of chart profiles. Kept as a literal so a
// newly added profile that forgets the utilization block fails here loudly
// rather than being silently skipped by a directory glob.
var shippedProfiles = []string{"a100", "h100", "b200", "gb200", "gb300", "l40s", "t4"}

// TestProfiles_ReportNonZeroUtilization is the regression guard for #506 item 3.
//
// The bug it catches: every shipped profile set `utilization.gpu: 0` and no
// profile carried a live `dynamic_metrics` block, so GetUtilizationRates
// returned a constant 0 on a default install. DCGM_FI_DEV_GPU_UTIL read 0, and
// because the mock derives every DCGM_FI_PROF_* activity metric as a fixed
// fraction of utilization.gpu, the whole profiling surface collapsed to 0 with
// it. Nothing caught this, because the e2e harness always installs the chart
// with gpu.dynamicMetrics.enabled=true and pins utilization to 50 — the
// shipped default was never the thing under test.
//
// This drives the real getter on a device built from the profile rather than
// asserting on YAML shape, so it also fails if the simulator stops being
// installed at construction or GetUtilizationRates stops consulting it.
func TestProfiles_ReportNonZeroUtilization(t *testing.T) {
	for _, name := range shippedProfiles {
		t.Run(name, func(t *testing.T) {
			cfg, err := LoadYAMLConfig(filepath.Join(testdataDir(), name+".yaml"))
			require.NoError(t, err, "load %s profile", name)

			dm := cfg.DeviceDefaults.DynamicMetrics
			require.NotNil(t, dm,
				"%s ships no live dynamic_metrics block, so utilization is the static "+
					"profile value and DCGM_FI_DEV_GPU_UTIL reads 0 on a default install (#506)",
				name)
			require.NotNil(t, dm.Utilization, "%s dynamic_metrics carries no utilization sub-block", name)

			srv := dgxa100.New()
			base, ok := srv.Devices[0].(*mockserver.Device)
			require.True(t, ok, "unexpected base device type")
			dev := NewConfigurableDevice(0, base, &cfg.DeviceDefaults, "GPU-"+name, "0000:01:00.0", 0, nil)

			// Sample repeatedly: the value is redrawn per call, so a single
			// draw could land in range even from a wrong band.
			for i := 0; i < 32; i++ {
				util, ret := dev.GetUtilizationRates()
				require.Equal(t, nvml.SUCCESS, ret, "%s GetUtilizationRates", name)

				require.GreaterOrEqual(t, util.Gpu, profileUtilGPUMin,
					"%s reported utilization.gpu=%d, below the shipped floor; a floor of 0 is "+
						"exactly the regression this guard exists to catch", name, util.Gpu)
				require.LessOrEqual(t, util.Gpu, profileUtilGPUMax,
					"%s reported utilization.gpu=%d, above the shipped ceiling", name, util.Gpu)
				require.GreaterOrEqual(t, util.Memory, profileUtilMemMin,
					"%s reported utilization.memory=%d, below the shipped floor", name, util.Memory)
				require.LessOrEqual(t, util.Memory, profileUtilMemMax,
					"%s reported utilization.memory=%d, above the shipped ceiling", name, util.Memory)
			}
		})
	}
}
