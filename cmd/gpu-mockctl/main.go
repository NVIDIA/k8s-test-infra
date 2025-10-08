// Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
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
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	mockfs "github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockfs"
	mocktopo "github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocktopo"
)

func main() {
	cmd := &cli.Command{
		Name:  "gpu-mockctl",
		Usage: "Generate mock NVIDIA driver filesystem for testing",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "base",
				Value: "/run/nvidia/driver",
				Usage: "mock driver root directory",
			},
			&cli.StringFlag{
				Name: "machine",
				Value: func() string {
					if v := os.Getenv("MACHINE_TYPE"); v != "" {
						return v
					}
					return "dgxa100"
				}(),
				Usage: "machine type (only dgxa100 supported)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return run(
				cmd.String("base"),
				cmd.String("machine"),
			)
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

func run(base, machine string) error {
	topo, err := mocktopo.New(machine)
	if err != nil {
		if os.Getenv("ALLOW_UNSUPPORTED") == "true" {
			log.Printf("unsupported machine %q, using fallback", machine)
			topo = mocktopo.NewFallback(8, "NVIDIA A100-SXM4-40GB")
		} else {
			return fmt.Errorf("failed to create topology: %w", err)
		}
	}

	layout := mockfs.Layout{Base: filepath.Clean(base)}
	for _, g := range topo.GPUs {
		layout.GPUs = append(layout.GPUs, mockfs.GPU{
			PCI:   mockfs.NormPCI(g.PCI),
			UUID:  g.UUID,
			Model: g.Model,
		})
	}

	if err := layout.Write(); err != nil {
		return fmt.Errorf("failed to write mock filesystem: %w", err)
	}

	log.Printf(
		"mock filesystem written under %s (%d GPUs)\n",
		layout.Base,
		len(layout.GPUs),
	)
	return nil
}
