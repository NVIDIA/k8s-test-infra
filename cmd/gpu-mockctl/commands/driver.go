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

	"github.com/urfave/cli/v3"

	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/internal/logger"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockdriver"
)

// NewDriverCommand creates the 'driver' subcommand
func NewDriverCommand(cfg *gpuconfig.Config) *cli.Command {
	return &cli.Command{
		Name:  "driver",
		Usage: "Deploy mock driver libraries and binaries",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "driver-root",
				Usage:       "root directory for driver files",
				Value:       "/var/lib/nvidia-mock/driver",
				Destination: &cfg.DriverRoot,
			},
			&cli.BoolFlag{
				Name:  "with-compiled-nvml",
				Usage: "use compiled mock NVML library instead of empty file",
				Value: true,
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Validate configuration
			if cfg.DriverRoot == "" {
				return ctx, fmt.Errorf("driver-root cannot be empty")
			}
			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			log := getLogger(cmd)
			withCompiledNVML := cmd.Bool("with-compiled-nvml")
			return runDriver(cfg, log, withCompiledNVML)
		},
	}
}

func runDriver(cfg *gpuconfig.Config, log logger.Interface, withCompiledNVML bool) error {
	log.Infof("Deploying mock driver files to: %s", cfg.DriverRoot)

	// Get appropriate file list based on whether we're using compiled NVML
	var files []mockdriver.FileSpec
	if withCompiledNVML {
		// Check if compiled NVML library is available
		nvmlSource := mockdriver.NVMLLibrarySource{
			SourcePath: "/usr/local/lib/mocknvml",
		}

		// Check if the compiled library exists
		if _, err := os.Stat(nvmlSource.SourcePath); err == nil {
			log.Infof("Using compiled mock NVML library from: %s", nvmlSource.SourcePath)

			// Deploy the compiled NVML library
			if err := mockdriver.DeployNVMLLibrary(nvmlSource, cfg.DriverRoot); err != nil {
				return fmt.Errorf("failed to deploy NVML library: %w", err)
			}

			// Get file list without NVML files (they were deployed separately)
			files = mockdriver.DefaultFilesWithNVML(cfg.DriverRoot)
		} else {
			log.Warningf("Compiled NVML library not found at %s, using empty files", nvmlSource.SourcePath)
			files = mockdriver.DefaultFiles(cfg.DriverRoot)
		}
	} else {
		// Use empty files for all libraries including NVML
		files = mockdriver.DefaultFiles(cfg.DriverRoot)
	}

	// Deploy device nodes if running with test environment variable
	if os.Getenv("__NVCT_TESTING_DEVICES_ARE_FILES") == "true" {
		log.Infof("Creating device nodes as files (test mode)")
		deviceFiles := mockdriver.DeviceNodes(cfg.DriverRoot, 8, false)
		files = append(files, deviceFiles...)
	}

	// Write all files with progress tracking
	totalFiles := len(files)
	log.WithField("count", totalFiles).Debug("Writing mock driver files")

	if err := mockdriver.WriteAll(files); err != nil {
		return fmt.Errorf("failed to write driver files: %w", err)
	}

	log.WithField("files", totalFiles).Info("Mock driver deployment complete")
	return nil
}
