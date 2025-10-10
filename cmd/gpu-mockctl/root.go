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

package main

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/commands"
	gpuconfig "github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/config"
	"github.com/NVIDIA/k8s-test-infra/cmd/gpu-mockctl/internal/logger"
)

const (
	appName = "gpu-mockctl"
)

// New creates the root command
func New() *cli.Command {
	cfg := gpuconfig.NewDefault()

	return &cli.Command{
		Name:  appName,
		Usage: "Generate mock NVIDIA driver filesystem for testing",
		Commands: []*cli.Command{
			commands.NewFSCommand(cfg),
			commands.NewCDICommand(cfg),
			commands.NewVersionCommand(),
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "verbose",
				Aliases:     []string{"v"},
				Usage:       "enable verbose logging",
				Destination: &cfg.Verbose,
			},
			&cli.StringFlag{
				Name:        "machine",
				Usage:       "machine type (only dgxa100 supported)",
				Value:       cfg.Machine,
				Destination: &cfg.Machine,
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Initialize logger for subcommands
			log := logger.New(appName, cfg.Verbose)

			// Initialize metadata if needed
			if cmd.Metadata == nil {
				cmd.Metadata = make(map[string]interface{})
			}
			cmd.Metadata["logger"] = log
			cmd.Metadata["config"] = cfg

			return ctx, cfg.Validate()
		},
	}
}

// getLogger retrieves logger from command context
func getLogger(cmd *cli.Command) logger.Interface {
	if log, ok := cmd.Metadata["logger"].(logger.Interface); ok {
		return log
	}
	// Fallback logger
	return logger.New(appName, false)
}

// getConfig retrieves config from command context
func getConfig(cmd *cli.Command) *gpuconfig.Config {
	if cfg, ok := cmd.Metadata["config"].(*gpuconfig.Config); ok {
		return cfg
	}
	// Fallback config
	return gpuconfig.NewDefault()
}
