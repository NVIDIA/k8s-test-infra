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

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	"sigs.k8s.io/yaml"

	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
)

// CDISpec represents a simplified CDI specification
type CDISpec struct {
	Version string      `json:"cdiVersion" yaml:"cdiVersion"`
	Kind    string      `json:"kind" yaml:"kind"`
	Devices []CDIDevice `json:"devices" yaml:"devices"`
}

// CDIDevice represents a device in the CDI spec
type CDIDevice struct {
	Name           string              `json:"name" yaml:"name"`
	ContainerEdits CDIContainerEdits   `json:"containerEdits" yaml:"containerEdits"`
	Annotations    map[string]string   `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// CDIContainerEdits represents container edits from CDI spec
type CDIContainerEdits struct {
	DeviceNodes []CDIDeviceNode `json:"deviceNodes,omitempty" yaml:"deviceNodes,omitempty"`
	Mounts      []CDIMount      `json:"mounts,omitempty" yaml:"mounts,omitempty"`
	Env         []string        `json:"env,omitempty" yaml:"env,omitempty"`
}

// CDIDeviceNode represents a device node in CDI spec
type CDIDeviceNode struct {
	Path     string   `json:"path" yaml:"path"`
	Type     string   `json:"type,omitempty" yaml:"type,omitempty"`
	Major    int64    `json:"major,omitempty" yaml:"major,omitempty"`
	Minor    int64    `json:"minor,omitempty" yaml:"minor,omitempty"`
	FileMode *uint32  `json:"fileMode,omitempty" yaml:"fileMode,omitempty"`
	UID      *uint32  `json:"uid,omitempty" yaml:"uid,omitempty"`
	GID      *uint32  `json:"gid,omitempty" yaml:"gid,omitempty"`
}

// CDIMount represents a mount in CDI spec
type CDIMount struct {
	HostPath      string   `json:"hostPath" yaml:"hostPath"`
	ContainerPath string   `json:"containerPath" yaml:"containerPath"`
	Type          string   `json:"type,omitempty" yaml:"type,omitempty"`
	Options       []string `json:"options,omitempty" yaml:"options,omitempty"`
}

// MockConfig represents the parsed configuration for the mock infrastructure
type MockConfig struct {
	GPUCount     int               `json:"gpuCount"`
	Architecture string            `json:"architecture"`
	DeviceNodes  []DeviceNodeSpec  `json:"deviceNodes"`
	ProcEntries  []ProcEntrySpec   `json:"procEntries"`
	Mounts       []MountSpec       `json:"mounts"`
	Environment  map[string]string `json:"environment"`
}

// DeviceNodeSpec represents a device node to be created
type DeviceNodeSpec struct {
	Path  string `json:"path"`
	Type  string `json:"type"`
	Major int64  `json:"major"`
	Minor int64  `json:"minor"`
	Mode  uint32 `json:"mode"`
}

// ProcEntrySpec represents a /proc entry to be created
type ProcEntrySpec struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// MountSpec represents a mount point
type MountSpec struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Type   string   `json:"type"`
	Options []string `json:"options"`
}

// NewCDICommand creates the CDI parsing command
func NewCDICommand(cfg *gpuconfig.Config) *cli.Command {
	return &cli.Command{
		Name:  "cdi",
		Usage: "Parse CDI spec and generate mock infrastructure configuration",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "spec",
				Usage:    "path to CDI spec file (YAML or JSON)",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "output",
				Usage: "output file for mock configuration (default: stdout)",
				Value: "-",
			},
			&cli.StringFlag{
				Name:  "architecture",
				Usage: "override GPU architecture (dgxa100, h100, h200, b200)",
				Value: "dgxa100",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runCDIParse(ctx, cmd, cfg)
		},
	}
}

func runCDIParse(ctx context.Context, cmd *cli.Command, cfg *gpuconfig.Config) error {
	specPath := cmd.String("spec")
	outputPath := cmd.String("output")
	arch := cmd.String("architecture")

	// Read CDI spec file
	data, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("failed to read CDI spec: %w", err)
	}

	// Parse CDI spec (support both YAML and JSON)
	var cdiSpec CDISpec
	if strings.HasSuffix(specPath, ".yaml") || strings.HasSuffix(specPath, ".yml") {
		if err := yaml.Unmarshal(data, &cdiSpec); err != nil {
			return fmt.Errorf("failed to parse CDI YAML spec: %w", err)
		}
	} else {
		if err := json.Unmarshal(data, &cdiSpec); err != nil {
			return fmt.Errorf("failed to parse CDI JSON spec: %w", err)
		}
	}

	// Convert CDI spec to mock configuration
	mockConfig := convertCDIToMockConfig(&cdiSpec, arch)

	// Serialize mock configuration
	configJSON, err := json.MarshalIndent(mockConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize mock config: %w", err)
	}

	// Write output
	if outputPath == "-" {
		fmt.Println(string(configJSON))
	} else {
		if err := os.WriteFile(outputPath, configJSON, 0644); err != nil {
			return fmt.Errorf("failed to write mock config: %w", err)
		}
	}

	return nil
}

func convertCDIToMockConfig(cdiSpec *CDISpec, arch string) *MockConfig {
	config := &MockConfig{
		GPUCount:     len(cdiSpec.Devices),
		Architecture: arch,
		DeviceNodes:  []DeviceNodeSpec{},
		ProcEntries:  []ProcEntrySpec{},
		Mounts:       []MountSpec{},
		Environment:  make(map[string]string),
	}

	// Extract architecture from annotations if available
	for _, device := range cdiSpec.Devices {
		if model, ok := device.Annotations["nvidia.com/gpu.model"]; ok {
			config.Architecture = detectArchFromModel(model)
			break
		}
	}

	// Process devices
	for i, device := range cdiSpec.Devices {
		// Process device nodes
		for _, devNode := range device.ContainerEdits.DeviceNodes {
			mode := uint32(0666)
			if devNode.FileMode != nil {
				mode = *devNode.FileMode
			}
			
			config.DeviceNodes = append(config.DeviceNodes, DeviceNodeSpec{
				Path:  devNode.Path,
				Type:  devNode.Type,
				Major: devNode.Major,
				Minor: devNode.Minor,
				Mode:  mode,
			})
		}

		// Process mounts
		for _, mount := range device.ContainerEdits.Mounts {
			config.Mounts = append(config.Mounts, MountSpec{
				Source:  mount.HostPath,
				Target:  mount.ContainerPath,
				Type:    mount.Type,
				Options: mount.Options,
			})
		}

		// Process environment variables
		for _, env := range device.ContainerEdits.Env {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				config.Environment[parts[0]] = parts[1]
			}
		}

		// Generate /proc entries for GPU
		gpuID := fmt.Sprintf("%08d", i)
		config.ProcEntries = append(config.ProcEntries, ProcEntrySpec{
			Path:    filepath.Join("/proc/driver/nvidia/gpus", gpuID, "information"),
			Content: generateGPUInfo(device.Name, i),
		})
	}

	// Add control device if not present
	hasCtl := false
	for _, node := range config.DeviceNodes {
		if node.Path == "/dev/nvidiactl" {
			hasCtl = true
			break
		}
	}
	if !hasCtl {
		config.DeviceNodes = append([]DeviceNodeSpec{{
			Path:  "/dev/nvidiactl",
			Type:  "c",
			Major: 195,
			Minor: 255,
			Mode:  0666,
		}}, config.DeviceNodes...)
	}

	// Add UVM devices if not present
	hasUVM := false
	for _, node := range config.DeviceNodes {
		if node.Path == "/dev/nvidia-uvm" {
			hasUVM = true
			break
		}
	}
	if !hasUVM {
		config.DeviceNodes = append(config.DeviceNodes, 
			DeviceNodeSpec{
				Path:  "/dev/nvidia-uvm",
				Type:  "c",
				Major: 510,
				Minor: 0,
				Mode:  0666,
			},
			DeviceNodeSpec{
				Path:  "/dev/nvidia-uvm-tools",
				Type:  "c",
				Major: 510,
				Minor: 1,
				Mode:  0666,
			},
		)
	}

	return config
}

func detectArchFromModel(model string) string {
	model = strings.ToLower(model)
	switch {
	case strings.Contains(model, "a100"):
		return "dgxa100"
	case strings.Contains(model, "h100"):
		return "h100"
	case strings.Contains(model, "h200"):
		return "h200"
	case strings.Contains(model, "b200"):
		return "b200"
	default:
		return "dgxa100"
	}
}

func generateGPUInfo(name string, index int) string {
	return fmt.Sprintf(`Model: %s
IRQ:   %d
GPU UUID: GPU-00000000-0000-0000-0000-%012d
Video BIOS: 96.00.00.00.00
Bus Type: PCIe
DMA Size: 47 bits
DMA Mask: 0x7fffffffffff
Bus Location: 0000:%02x:00.0
Device Minor: %d
`, name, 100+index, index, index, index)
}

