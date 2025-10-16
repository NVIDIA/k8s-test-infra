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
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockfs"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocktopo"
)

// NewFSCommand creates the 'fs' subcommand
func NewFSCommand(cfg *gpuconfig.Config) *cli.Command {
	return &cli.Command{
		Name:  "fs",
		Usage: "Generate mock driver filesystem under proc and dev",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "base",
				Usage:       "mock driver root directory",
				Value:       cfg.Base,
				Destination: &cfg.Base,
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Validate configuration
			if err := cfg.ValidateFS(); err != nil {
				return ctx, err
			}
			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			log := getLogger(cmd)
			return runFS(cfg, log)
		},
	}
}

// getLogger retrieves logger from command metadata
func getLogger(cmd *cli.Command) logger.Interface {
	// Try to get from parent metadata first
	var parent = cmd
	for parent != nil {
		if parent.Metadata != nil {
			if log, ok := parent.Metadata["logger"].(logger.Interface); ok {
				return log
			}
		}
		// Check if we can access parent through Root
		if parent.Root() != nil && parent.Root() != parent {
			parent = parent.Root()
		} else {
			break
		}
	}
	// Fallback logger
	return logger.New("gpu-mockctl", false)
}

func runFS(cfg *gpuconfig.Config, log logger.Interface) error {
	log.Infof("Creating mock filesystem for machine: %s", cfg.Machine)
	log.Debugf("Base directory: %s", cfg.Base)

	// Get topology
	topo, err := mocktopo.New(cfg.Machine)
	if err != nil {
		if os.Getenv("ALLOW_UNSUPPORTED") == "true" {
			log.Warningf("Unsupported machine %q, using fallback", cfg.Machine)
			topo = mocktopo.NewFallback(8, "NVIDIA A100-SXM4-40GB")
		} else {
			return fmt.Errorf("failed to create topology: %w", err)
		}
	}

	// Create layout
	layout := mockfs.Layout{Base: filepath.Clean(cfg.Base)}
	for _, g := range topo.GPUs {
		layout.GPUs = append(layout.GPUs, mockfs.GPU{
			PCI:   mockfs.NormPCI(g.PCI),
			UUID:  g.UUID,
			Model: g.Model,
		})
		log.Debugf("Adding GPU: PCI=%s, UUID=%s, Model=%s", g.PCI, g.UUID, g.Model)
	}

	log.Debugf("Writing %d GPU entries to %s", len(layout.GPUs), layout.Base)

	if err := layout.Write(); err != nil {
		return fmt.Errorf("failed to write mock filesystem: %w", err)
	}

	log.Infof("Mock filesystem written under %s (%d GPUs)",
		layout.Base, len(layout.GPUs))
	return nil
}
