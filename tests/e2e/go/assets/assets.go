//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package assets embeds the Kubernetes manifests the scenarios need, so the
// harness binary is cwd-independent (removes a local/CI drift source). Kind
// cluster configs used to be embedded here too but are no longer used — the
// external owner (Tilt / `make cluster-create`) provisions the cluster.
package assets

import (
	_ "embed"
	"os"
)

//go:embed device-plugin-mock.yaml
var DevicePluginManifest []byte

//go:embed nri-gpu-agent.yaml
var NRIGpuAgentManifest []byte

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
