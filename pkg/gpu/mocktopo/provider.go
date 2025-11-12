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

package mocktopo

import (
	"errors"
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	dgxa100 "github.com/NVIDIA/go-nvml/pkg/nvml/mock/dgxa100"
)

// GPUInfo holds identifying information for a single GPU.
type GPUInfo struct {
	PCI   string
	UUID  string
	Model string
}

// Topology represents the GPU topology for a given machine type.
// It includes both high-level GPU information and the underlying NVML
// interface for integration with nvidia-container-toolkit.
type Topology struct {
	GPUs     []GPUInfo
	nvmlImpl nvml.Interface
}

// NVMLInterface returns the underlying NVML implementation.
// This is used by the CDI generator to query device information.
func (t *Topology) NVMLInterface() nvml.Interface {
	return t.nvmlImpl
}

// FlavorProvider is a function that constructs a Topology for a specific
// machine flavor (e.g., dgxa100, dgxh100).
type FlavorProvider func() (*Topology, error)

var registry = map[string]FlavorProvider{}

// Register adds a new flavor provider to the registry.
func Register(name string, fn FlavorProvider) {
	registry[name] = fn
}

func init() {
	Register("dgxa100", func() (*Topology, error) {
		// Create wrapped NVML interface with MIG support stubs
		nvmlImpl := newNVMLWrapper()

		// Extract GPU info from the underlying dgxa100 server
		srv := dgxa100.New()
		var gpus []GPUInfo
		for _, d := range srv.Devices {
			dev := d.(*dgxa100.Device)
			gpus = append(gpus, GPUInfo{
				PCI:   dev.PciBusID,
				UUID:  dev.UUID,
				Model: dev.Name,
			})
		}
		if len(gpus) == 0 {
			return nil, errors.New("dgxa100 mock returned zero GPUs")
		}
		return &Topology{
			GPUs:     gpus,
			nvmlImpl: nvmlImpl, // Store the wrapped NVML interface
		}, nil
	})
}

// New returns a Topology for the specified machine type, or an error if
// the machine type is unsupported.
func New(machine string) (*Topology, error) {
	if fn, ok := registry[machine]; ok {
		return fn()
	}
	return nil, fmt.Errorf(
		"unsupported MACHINE_TYPE %q (only 'dgxa100'); set "+
			"ALLOW_UNSUPPORTED=true to use fallback",
		machine,
	)
}
