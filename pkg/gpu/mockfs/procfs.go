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

package mockfs

import (
	"fmt"
	"os"
	"path/filepath"
)

// GPU represents a single GPU device in the mock filesystem.
type GPU struct {
	PCI   string
	UUID  string
	Model string
}

// Layout represents the complete mock NVIDIA driver filesystem layout.
type Layout struct {
	Base string
	GPUs []GPU
}

// Write creates the mock NVIDIA driver filesystem structure under Base,
// including device nodes in dev/ and proc entries in proc/driver/nvidia/.
func (l Layout) Write() error {
	dev := filepath.Join(l.Base, "dev")
	proc := filepath.Join(l.Base, "proc/driver/nvidia")

	if err := os.MkdirAll(dev, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(proc, 0o755); err != nil {
		return err
	}

	// Create device nodes for each GPU
	for i := range l.GPUs {
		devPath := filepath.Join(dev, fmt.Sprintf("nvidia%d", i))
		if err := MkChar(devPath, 195, uint32(i), 0o666); err != nil {
			return err
		}
	}

	// Create control and UVM device nodes
	if err := MkChar(filepath.Join(dev, "nvidiactl"), 195, 255, 0o666); err != nil {
		return err
	}
	if err := MkChar(filepath.Join(dev, "nvidia-uvm"), 235, 0, 0o666); err != nil {
		return err
	}
	if err := MkChar(filepath.Join(dev, "nvidia-uvm-tools"), 235, 1, 0o666); err != nil {
		return err
	}

	// Write proc version file
	versionContent := []byte("NVRM version: mock (go-nvml dgxa100)\n")
	versionPath := filepath.Join(proc, "version")
	if err := os.WriteFile(versionPath, versionContent, 0o644); err != nil {
		return err
	}

	// Write GPU information files
	for _, g := range l.GPUs {
		gpuDir := filepath.Join(proc, "gpus", g.PCI)
		if err := os.MkdirAll(gpuDir, 0o755); err != nil {
			return err
		}
		info := fmt.Sprintf("Model: %s\nGPU UUID: %s\nBus Location: %s\n",
			g.Model, g.UUID, g.PCI)
		infoPath := filepath.Join(gpuDir, "information")
		if err := os.WriteFile(infoPath, []byte(info), 0o644); err != nil {
			return err
		}
	}

	return nil
}
