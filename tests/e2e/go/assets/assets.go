//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package assets embeds the Kind cluster configs and Kubernetes manifests the
// scenarios need, so the harness binary is cwd-independent (removes a
// local/CI drift source). These are COPIES of the files under tests/e2e/; the
// originals are retained until the bash jobs are deleted in a follow-up
// (staged migration).
package assets

import (
	_ "embed"
	"os"
)

// KindDRAConfig is the embedded Kind cluster config for the DRA scenario.
//
//go:embed kind-dra-config.yaml
var KindDRAConfig []byte

// KindGPUOperatorConfig is the embedded Kind cluster config for the GPU Operator scenario.
//
//go:embed kind-gpu-operator-config.yaml
var KindGPUOperatorConfig []byte

// KindMultiNodeConfig is the embedded Kind cluster config for the heterogeneous multi-node scenario.
//
//go:embed kind-multi-node-config.yaml
var KindMultiNodeConfig []byte

// KindNRIConfig is the embedded Kind cluster config for the NRI ambient-injection scenario.
//
//go:embed kind-nri-config.yaml
var KindNRIConfig []byte

// DevicePluginManifest is the embedded device-plugin manifest used by the standalone scenario.
//
//go:embed device-plugin-mock.yaml
var DevicePluginManifest []byte

// NRIGpuAgentManifest is the embedded NRI plugin manifest that injects nvml-mock.
//
//go:embed nri-gpu-agent.yaml
var NRIGpuAgentManifest []byte

// GPUOperatorValues is the embedded Helm values override for the GPU Operator scenario.
//
//go:embed gpu-operator-values.yaml
var GPUOperatorValues []byte

// GFDManifest is the embedded GPU Feature Discovery DaemonSet manifest used by the NFD scenario.
//
//go:embed gfd-mock.yaml
var GFDManifest []byte

// ValidatorManifest is the embedded GPU Operator validator manifest used by the standalone scenario.
//
//go:embed validator-mock.yaml
var ValidatorManifest []byte

// WriteTemp writes content to a temp file with the given pattern and returns
// the path. Used for `helm install -f <values>` which needs a file path.
func WriteTemp(pattern string, content []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}
