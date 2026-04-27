// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package config defines the YAML schema for the InfiniBand block embedded
// in mock-nvml profile configs. The renderer consumes this to populate the
// fake sysfs tree under MOCK_IB_ROOT.
package config

// Profile is the minimal slice of the mock-nvml profile YAML that the
// InfiniBand renderer cares about. It deliberately ignores all GPU fields.
type Profile struct {
	Infiniband Infiniband `json:"infiniband" yaml:"infiniband"`
}

// Infiniband describes the simulated HCA topology rendered into sysfs. All
// fields have sensible zero-value defaults applied by ApplyDefaults so that
// minimal profile blocks (e.g. {enabled: true}) still produce realistic
// output.
type Infiniband struct {
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Per-CA static metadata.
	HCAType   string `json:"hca_type"   yaml:"hca_type"`   // e.g. "MT4129" (ConnectX-7)
	FWVersion string `json:"fw_version" yaml:"fw_version"` // e.g. "28.39.2048"
	HWRev     string `json:"hw_rev"     yaml:"hw_rev"`     // e.g. "0x0"
	BoardID   string `json:"board_id"   yaml:"board_id"`   // e.g. "MT_0000000838"

	// NodeDescTemplate supports placeholders {node_name} and {idx}.
	NodeDescTemplate string `json:"node_desc_template" yaml:"node_desc_template"`

	// Per-port settings.
	LinkLayer string `json:"link_layer" yaml:"link_layer"` // "InfiniBand" | "Ethernet"
	RateGbps  int    `json:"rate_gbps"  yaml:"rate_gbps"`  // 100, 200, 400, ...
	PortState string `json:"port_state" yaml:"port_state"` // "ACTIVE" | "INIT" | "DOWN"
	PhysState string `json:"phys_state" yaml:"phys_state"` // "LinkUp" | "Disabled" | ...

	// Topology shape.
	HCAsPerGPU int `json:"hcas_per_gpu" yaml:"hcas_per_gpu"` // total = gpu_count * hcas_per_gpu
	HCACountOverride int `json:"hca_count" yaml:"hca_count"` // if >0, used instead of gpu_count*hcas_per_gpu

	// GUIDPrefix is the upper 6 bytes of every node/port GUID, in hex
	// (with optional ':' separators). The lower 2 bytes encode the HCA
	// index. Example: "a088c2:0300:ab" -> "a088c20300ab" -> per-HCA GUID
	// "a088:c203:00ab:00<idx>".
	GUIDPrefix string `json:"guid_prefix" yaml:"guid_prefix"`
}

// Defaults returns a copy of the InfiniBand block with reasonable fallback
// values applied for any unset string/int fields. The returned value is safe
// to render directly.
func (ib Infiniband) Defaults() Infiniband {
	out := ib
	if out.HCAType == "" {
		out.HCAType = "MT4129" // ConnectX-7 NDR
	}
	if out.FWVersion == "" {
		out.FWVersion = "28.39.2048"
	}
	if out.HWRev == "" {
		out.HWRev = "0x0"
	}
	if out.BoardID == "" {
		out.BoardID = "MT_0000000838"
	}
	if out.NodeDescTemplate == "" {
		out.NodeDescTemplate = "{node_name} mlx5_{idx}"
	}
	if out.LinkLayer == "" {
		out.LinkLayer = "InfiniBand"
	}
	if out.RateGbps == 0 {
		out.RateGbps = 400
	}
	if out.PortState == "" {
		out.PortState = "ACTIVE"
	}
	if out.PhysState == "" {
		out.PhysState = "LinkUp"
	}
	if out.HCAsPerGPU == 0 {
		out.HCAsPerGPU = 1
	}
	if out.GUIDPrefix == "" {
		out.GUIDPrefix = "a088c20300ab"
	}
	return out
}
