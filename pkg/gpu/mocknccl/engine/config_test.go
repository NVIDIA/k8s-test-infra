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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadConfigDerivesBandwidth(t *testing.T) {
	path := writeProfile(t, `
nvlink:
  links_per_gpu: 18
  bandwidth_per_link_gbps: 25
infiniband:
  rate_gbps: 400
  hca_count: 4
nccl:
  enabled: true
  latency_us: 6
  efficiency: 0.9
`)

	m := LoadConfig(path).Model()
	require.Equal(t, 200e9, m.InterBytesPerSec)
	require.Equal(t, 450e9, m.IntraBytesPerSec)
	require.Equal(t, 6.0, m.LatencyUS)
	require.Equal(t, 0.9, m.Efficiency)
}

func TestEnvOverridesWin(t *testing.T) {
	t.Setenv("MOCK_NCCL_INTER_GBPS", "800")
	t.Setenv("MOCK_NCCL_LATENCY_US", "3")

	path := writeProfile(t, `
infiniband:
  rate_gbps: 400
  hca_count: 1
nccl:
  enabled: true
`)

	m := LoadConfig(path).Model()
	require.Equal(t, 800e9/8, m.InterBytesPerSec)
	require.Equal(t, 3.0, m.LatencyUS)
}

func TestDefaultsWhenAbsent(t *testing.T) {
	cfg := LoadConfig("")
	m := cfg.Model()
	require.Equal(t, DefaultEfficiency, m.Efficiency)
	require.Equal(t, DefaultLatencyUS, m.LatencyUS)
	require.Equal(t, DefaultVersion, cfg.Version)
}

func TestNCCLBlockOverridesDerivedBandwidth(t *testing.T) {
	path := writeProfile(t, `
nvlink:
  links_per_gpu: 18
  bandwidth_per_link_gbps: 25
infiniband:
  rate_gbps: 400
  hca_count: 4
nccl:
  enabled: true
  intra_node_bandwidth_gbps: 800
  inter_node_bandwidth_gbps: 1600
`)

	m := LoadConfig(path).Model()
	require.Equal(t, 800e9/8, m.IntraBytesPerSec)
	require.Equal(t, 1600e9/8, m.InterBytesPerSec)
}

func TestHCAsPerGPUFallback(t *testing.T) {
	path := writeProfile(t, `
infiniband:
  rate_gbps: 400
  hcas_per_gpu: 2
`)

	m := LoadConfig(path).Model()
	require.Equal(t, 400e9/8*2, m.InterBytesPerSec)
}

func TestEnabledTriState(t *testing.T) {
	t.Run("explicitly disabled", func(t *testing.T) {
		path := writeProfile(t, `
nccl:
  enabled: false
`)
		require.False(t, LoadConfig(path).Enabled)
	})

	t.Run("omitted defaults to enabled", func(t *testing.T) {
		path := writeProfile(t, `
nccl:
  latency_us: 5
`)
		require.True(t, LoadConfig(path).Enabled)
	})
}
