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
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/internal/logger"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/cdi"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockdriver"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocktopo"
)

// NewCDICommand creates the 'cdi' subcommand
func NewCDICommand(cfg *gpuconfig.Config) *cli.Command {
	return &cli.Command{
		Name:  "cdi",
		Usage: "Generate mock driver tree and CDI specification",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "driver-root",
				Usage:       "host mock driver tree root",
				Value:       cfg.DriverRoot,
				Destination: &cfg.DriverRoot,
			},
			&cli.StringFlag{
				Name:        "cdi-output",
				Usage:       "CDI spec output path",
				Value:       cfg.CDIOutput,
				Destination: &cfg.CDIOutput,
			},
			&cli.BoolFlag{
				Name:        "with-dri",
				Usage:       "include DRI render node",
				Destination: &cfg.WithDRI,
			},
			&cli.BoolFlag{
				Name:        "with-hook",
				Usage:       "include CDI hook references",
				Destination: &cfg.WithHook,
			},
			&cli.StringFlag{
				Name:        "toolkit-root",
				Usage:       "toolkit root for hook paths",
				Value:       cfg.ToolkitRoot,
				Destination: &cfg.ToolkitRoot,
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Validate configuration
			if err := cfg.ValidateCDI(); err != nil {
				return ctx, err
			}
			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			log := getLogger(cmd)
			return runCDI(cfg, log)
		},
	}
}

func runCDI(cfg *gpuconfig.Config, log logger.Interface) error {
	log.Infof("Generating CDI specification for machine: %s", cfg.Machine)
	log.Debugf("Driver root: %s", cfg.DriverRoot)
	log.Debugf("CDI output: %s", cfg.CDIOutput)

	// Get topology
	topo, err := mocktopo.New(cfg.Machine)
	if err != nil {
		if os.Getenv("ALLOW_UNSUPPORTED") == "true" {
			log.Warningf("Using fallback mock for CDI generation")
			topo = mocktopo.NewFallback(8, "NVIDIA A100-SXM4-40GB")
		} else {
			return fmt.Errorf("failed to create topology: %w", err)
		}
	}

	gpuCount := len(topo.GPUs)
	log.Debugf("GPU count: %d", gpuCount)

	// Create mock driver tree
	log.Debugf("Writing mock driver files to %s", cfg.DriverRoot)
	files := mockdriver.DefaultFiles(cfg.DriverRoot)
	if err := mockdriver.WriteAll(files); err != nil {
		return fmt.Errorf("failed to write driver files: %w", err)
	}
	log.Infof("Mock driver tree written to %s", cfg.DriverRoot)

	// Create device nodes
	if err := createDeviceNodes(cfg, gpuCount, log); err != nil {
		// Log warnings but don't fail - device nodes might already exist
		log.Warningf("Device node creation had errors: %v", err)
	}

	// Generate CDI spec using nvidia-container-toolkit nvcdi library
	log.Debugf("Generating CDI specification")
	cdiOpts := cdi.Options{
		NVMLLib:           topo.NVMLInterface(),
		DriverRoot:        cfg.DriverRoot,
		DevRoot:           "/host/dev", // DevRoot is already prefixed by the DaemonSet mount
		NVIDIACDIHookPath: filepath.Join(cfg.ToolkitRoot, "bin/nvidia-cdi-hook"),
	}

	specYAML, err := cdi.Generate(cdiOpts)
	if err != nil {
		return fmt.Errorf("failed to generate CDI spec: %w", err)
	}

	// Validate before writing
	log.Debugf("Validating CDI specification")
	if err := cdi.Validate(specYAML); err != nil {
		return fmt.Errorf("CDI spec validation failed: %w", err)
	}

	// Write CDI spec
	log.Debugf("Writing CDI spec to %s", cfg.CDIOutput)
	if err := os.MkdirAll(filepath.Dir(cfg.CDIOutput), 0o755); err != nil {
		return fmt.Errorf("failed to create CDI directory: %w", err)
	}

	if err := os.WriteFile(cfg.CDIOutput, specYAML, 0o644); err != nil {
		return fmt.Errorf("failed to write CDI spec: %w", err)
	}

	log.Infof("CDI spec written to %s (generated via nvidia-container-toolkit)",
		cfg.CDIOutput)
	return nil
}

func createDeviceNodes(cfg *gpuconfig.Config, gpuCount int, log logger.Interface) error {
	// Create device nodes (both host /dev and under driverRoot/dev)
	// Host /dev nodes for CDI runtime compatibility
	log.Debugf("Creating host device nodes in /dev")
	hostDevNodes := mockdriver.DeviceNodes("/dev", gpuCount, cfg.WithDRI)
	if err := mockdriver.WriteAll(hostDevNodes); err != nil {
		log.Warningf("Failed to create host /dev nodes: %v", err)
		// Don't return error, continue with driver root nodes
	}

	// Also create under driverRoot/dev for completeness
	log.Debugf("Creating device nodes under %s/dev", cfg.DriverRoot)
	driverDevNodes := mockdriver.DeviceNodes(cfg.DriverRoot, gpuCount, cfg.WithDRI)
	if err := mockdriver.WriteAll(driverDevNodes); err != nil {
		log.Warningf("Failed to create %s/dev nodes: %v", cfg.DriverRoot, err)
		// Don't return error as nodes might already exist
	}

	return nil
}
