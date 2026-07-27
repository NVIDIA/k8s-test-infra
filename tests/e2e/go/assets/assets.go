//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package assets embeds the Kind cluster configs and Kubernetes manifests the
// scenarios need, so the harness binary is cwd-independent (removes a
// local/CI drift source).
//
// Some baseline assets (device-plugin-mock.yaml, gfd-mock.yaml,
// gpu-operator-values.yaml, kind-*.yaml) are COPIES of human-facing files
// under tests/e2e/ that docs and the chart NOTES still reference by path.
//
// The GPU Operator managed-driver / host-driver / kmod overlays
// (gpu-operator-driver-values.yaml, gpu-operator-driver-kmod-values.yaml,
// gpu-operator-hostdriver-values.yaml) live ONLY here: they are consumed
// exclusively by the Go scenarios, so there is no top-level copy to drift
// against.
package assets

import (
	_ "embed"
	"os"
)

//go:embed kind-dra-config.yaml
var KindDRAConfig []byte

//go:embed kind-gpu-operator-config.yaml
var KindGPUOperatorConfig []byte

//go:embed kind-multi-node-config.yaml
var KindMultiNodeConfig []byte

//go:embed kind-nri-config.yaml
var KindNRIConfig []byte

//go:embed device-plugin-mock.yaml
var DevicePluginManifest []byte

//go:embed nri-gpu-agent.yaml
var NRIGpuAgentManifest []byte

//go:embed gpu-operator-values.yaml
var GPUOperatorValues []byte

//go:embed gpu-operator-driver-values.yaml
var GPUOperatorDriverValues []byte

//go:embed gpu-operator-driver-kmod-values.yaml
var GPUOperatorDriverKmodValues []byte

//go:embed gpu-operator-hostdriver-values.yaml
var GPUOperatorHostDriverValues []byte

//go:embed gfd-mock.yaml
var GFDManifest []byte

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
