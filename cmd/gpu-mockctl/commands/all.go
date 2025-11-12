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

	"github.com/urfave/cli/v3"

	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/internal/logger"
)

// NewAllCommand creates the 'all' subcommand that runs both fs and driver
func NewAllCommand(cfg *gpuconfig.Config) *cli.Command {
	return &cli.Command{
		Name:  "all",
		Usage: "Generate both mock filesystem and driver files",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "base",
				Usage:       "mock driver root directory for device nodes and proc files",
				Value:       cfg.Base,
				Destination: &cfg.Base,
			},
			&cli.StringFlag{
				Name:        "driver-root",
				Usage:       "root directory for driver libraries and binaries",
				Value:       "/var/lib/nvidia-mock/driver",
				Destination: &cfg.DriverRoot,
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Validate configuration
			if err := cfg.ValidateFS(); err != nil {
				return ctx, err
			}
			if cfg.DriverRoot == "" {
				return ctx, fmt.Errorf("driver-root cannot be empty")
			}
			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			log := getLogger(cmd)
			return runAll(cfg, log)
		},
	}
}

func runAll(cfg *gpuconfig.Config, log logger.Interface) error {
	log.Infof("Running complete mock setup")

	// Run filesystem creation first
	log.Infof("Step 1: Creating mock filesystem")
	if err := runFS(cfg, log); err != nil {
		return fmt.Errorf("failed to create filesystem: %w", err)
	}

	// Run driver deployment
	log.Infof("Step 2: Deploying mock driver files")
	if err := runDriver(cfg, log, true); err != nil {
		return fmt.Errorf("failed to deploy driver files: %w", err)
	}

	log.Infof("Successfully completed mock setup")
	log.Infof("  - Device nodes and proc files: %s", cfg.Base)
	log.Infof("  - Driver libraries and binaries: %s", cfg.DriverRoot)

	return nil
}

