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
	"strconv"

	"sigs.k8s.io/yaml"
)

// Defaults for the cost model when the profile/env do not specify them.
const (
	DefaultLatencyUS  = 6.0
	DefaultEfficiency = 0.90
	DefaultVersion    = 22304 // NCCL 2.23.4
	// Fallback bandwidths (bytes/sec) when neither profile nor env provide
	// derivable values, so a bare run still reports non-zero busbw.
	fallbackInterBytesPerSec = 400e9 / 8 // 400 Gbps single NDR HCA
	fallbackIntraBytesPerSec = 450e9     // 18 * 25 GB/s
)

// Config is the resolved mock NCCL configuration.
type Config struct {
	Enabled          bool
	Version          int
	latencyUS        float64
	efficiency       float64
	intraBytesPerSec float64
	interBytesPerSec float64
}

// profileYAML is the minimal slice of the mock-nvml profile this package reads.
type profileYAML struct {
	NVLink *struct {
		LinksPerGPU          int `json:"links_per_gpu"`
		BandwidthPerLinkGBPS int `json:"bandwidth_per_link_gbps"`
	} `json:"nvlink"`
	Infiniband *struct {
		Enabled  bool `json:"enabled"`
		RateGbps int  `json:"rate_gbps"`
		HCACount int  `json:"hca_count"`
		HCAsPer  int  `json:"hcas_per_gpu"`
	} `json:"infiniband"`
	NCCL *struct {
		Enabled    *bool   `json:"enabled"`
		Version    int     `json:"version"`
		LatencyUS  float64 `json:"latency_us"`
		Efficiency float64 `json:"efficiency"`
		IntraGbps  float64 `json:"intra_node_bandwidth_gbps"`
		InterGbps  float64 `json:"inter_node_bandwidth_gbps"`
	} `json:"nccl"`
}

// LoadConfig reads the profile YAML at path (empty => skip), then derives the
// cost-model bandwidths from the nvlink/infiniband blocks, applies any nccl
// block, and finally lets MOCK_NCCL_* env vars override. Missing inputs fall
// back to NDR/NVLink defaults so a bare run still produces non-zero busbw.
func LoadConfig(path string) Config {
	cfg := Config{
		Enabled:          true,
		Version:          DefaultVersion,
		latencyUS:        DefaultLatencyUS,
		efficiency:       DefaultEfficiency,
		intraBytesPerSec: fallbackIntraBytesPerSec,
		interBytesPerSec: fallbackInterBytesPerSec,
	}

	var p profileYAML
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = yaml.Unmarshal(b, &p)
		}
	}

	if p.NVLink != nil && p.NVLink.LinksPerGPU > 0 && p.NVLink.BandwidthPerLinkGBPS > 0 {
		// NVLink bandwidth_per_link_gbps is conventionally GB/s (bytes), so no /8.
		cfg.intraBytesPerSec = float64(p.NVLink.LinksPerGPU) * float64(p.NVLink.BandwidthPerLinkGBPS) * 1e9
	}
	if p.Infiniband != nil && p.Infiniband.RateGbps > 0 {
		hca := p.Infiniband.HCACount
		if hca <= 0 {
			hca = p.Infiniband.HCAsPer
		}
		if hca <= 0 {
			hca = 1
		}
		// IB rate_gbps is Gbit/s, so /8 converts to bytes/sec.
		cfg.interBytesPerSec = float64(p.Infiniband.RateGbps) * 1e9 / 8 * float64(hca)
	}
	if p.NCCL != nil {
		if p.NCCL.Enabled != nil {
			cfg.Enabled = *p.NCCL.Enabled
		}
		if p.NCCL.Version > 0 {
			cfg.Version = p.NCCL.Version
		}
		if p.NCCL.LatencyUS > 0 {
			cfg.latencyUS = p.NCCL.LatencyUS
		}
		if p.NCCL.Efficiency > 0 {
			cfg.efficiency = p.NCCL.Efficiency
		}
		if p.NCCL.IntraGbps > 0 {
			// nccl.intra_node_bandwidth_gbps is Gbit/s, so /8 converts to bytes/sec.
			cfg.intraBytesPerSec = p.NCCL.IntraGbps * 1e9 / 8
		}
		if p.NCCL.InterGbps > 0 {
			// nccl.inter_node_bandwidth_gbps is Gbit/s, so /8 converts to bytes/sec.
			cfg.interBytesPerSec = p.NCCL.InterGbps * 1e9 / 8
		}
	}

	applyEnvOverrides(&cfg)
	return cfg
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MOCK_NCCL_VERSION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Version = n
		}
	}
	if v := os.Getenv("MOCK_NCCL_LATENCY_US"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.latencyUS = f
		}
	}
	if v := os.Getenv("MOCK_NCCL_EFFICIENCY"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.efficiency = f
		}
	}
	// MOCK_NCCL_*_GBPS mirror the nccl block semantics: Gbit/s, so /8 -> bytes/sec.
	if v := os.Getenv("MOCK_NCCL_INTRA_GBPS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.intraBytesPerSec = f * 1e9 / 8
		}
	}
	if v := os.Getenv("MOCK_NCCL_INTER_GBPS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.interBytesPerSec = f * 1e9 / 8
		}
	}
}

// Model projects the resolved config into a cost Model.
func (c Config) Model() Model {
	return Model{
		LatencyUS:        c.latencyUS,
		Efficiency:       c.efficiency,
		IntraBytesPerSec: c.intraBytesPerSec,
		InterBytesPerSec: c.interBytesPerSec,
	}
}
