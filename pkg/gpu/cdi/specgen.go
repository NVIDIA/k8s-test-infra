// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

package cdi

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi"

	"sigs.k8s.io/yaml"
)

// Options contains configuration for CDI spec generation using the
// nvidia-container-toolkit nvcdi library.
type Options struct {
	// NVMLLib is the NVML library implementation to use for device
	// discovery. Typically a mock implementation for testing.
	NVMLLib nvml.Interface

	// DriverRoot is the root directory where the mock NVIDIA driver
	// files are located.
	DriverRoot string

	// DevRoot is the root directory where device nodes are located
	// (typically /dev or /host/dev).
	DevRoot string

	// NVIDIACDIHookPath is the path to the nvidia-cdi-hook binary.
	NVIDIACDIHookPath string

	// Vendor is the CDI vendor name (default: nvidia.com).
	Vendor string

	// Class is the CDI class name (default: gpu).
	Class string
}

// Generate creates a CDI specification using the nvidia-container-toolkit's
// nvcdi library with the provided NVML implementation (typically a mock).
//
// This leverages the production-grade CDI generation logic from
// nvidia-container-toolkit, ensuring consistency with real deployments.
func Generate(o Options) ([]byte, error) {
	if o.NVMLLib == nil {
		return nil, fmt.Errorf("NVMLLib is required")
	}

	if o.DriverRoot == "" {
		o.DriverRoot = "/"
	}

	if o.DevRoot == "" {
		o.DevRoot = o.DriverRoot
	}

	if o.NVIDIACDIHookPath == "" {
		o.NVIDIACDIHookPath = "/usr/bin/nvidia-cdi-hook"
	}

	// Build nvcdi options
	opts := []nvcdi.Option{
		nvcdi.WithMode(nvcdi.ModeNvml),
		nvcdi.WithNvmlLib(o.NVMLLib),
		nvcdi.WithDriverRoot(o.DriverRoot),
		nvcdi.WithDevRoot(o.DevRoot),
		nvcdi.WithNVIDIACDIHookPath(o.NVIDIACDIHookPath),
	}

	if o.Vendor != "" {
		opts = append(opts, nvcdi.WithVendor(o.Vendor))
	}
	if o.Class != "" {
		opts = append(opts, nvcdi.WithClass(o.Class))
	}

	// Create the nvcdi library with our mock NVML
	lib, err := nvcdi.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create nvcdi library: %w", err)
	}

	// Generate the CDI spec
	spec, err := lib.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("failed to generate CDI spec: %w", err)
	}

	// Get the raw spec and marshal to YAML
	rawSpec := spec.Raw()
	return yaml.Marshal(rawSpec)
}
