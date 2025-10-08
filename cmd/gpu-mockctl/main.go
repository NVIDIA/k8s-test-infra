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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/cdi"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockdriver"
	mockfs "github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockfs"
	mocktopo "github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocktopo"
)

func main() {
	cmd := &cli.Command{
		Name:  "gpu-mockctl",
		Usage: "Generate mock NVIDIA driver filesystem for testing",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "mode",
				Value: "all",
				Usage: "operation mode: fs, cdi, or all",
			},
			&cli.StringFlag{
				Name:  "base",
				Value: "/run/nvidia/driver",
				Usage: "mock driver root directory (for fs mode)",
			},
			&cli.StringFlag{
				Name:  "driver-root",
				Value: "/var/lib/nvidia-mock/driver",
				Usage: "host mock driver tree root (for cdi mode)",
			},
			&cli.StringFlag{
				Name:  "cdi-output",
				Value: cdi.DefaultSpecPath,
				Usage: "CDI spec output path",
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
			&cli.BoolFlag{
				Name:  "with-dri",
				Usage: "include DRI render node",
			},
			&cli.BoolFlag{
				Name:  "with-hook",
				Usage: "include CDI hook references",
			},
			&cli.StringFlag{
				Name:  "toolkit-root",
				Value: "/usr/local/nvidia-container-toolkit",
				Usage: "toolkit root for hook paths",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return run(&config{
				mode:        cmd.String("mode"),
				base:        cmd.String("base"),
				driverRoot:  cmd.String("driver-root"),
				cdiOutput:   cmd.String("cdi-output"),
				machine:     cmd.String("machine"),
				withDRI:     cmd.Bool("with-dri"),
				withHook:    cmd.Bool("with-hook"),
				toolkitRoot: cmd.String("toolkit-root"),
			})
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	mode        string
	base        string
	driverRoot  string
	cdiOutput   string
	machine     string
	withDRI     bool
	withHook    bool
	toolkitRoot string
}

func run(cfg *config) error {
	// Get topology (A100-only for now)
	topo, err := mocktopo.New(cfg.machine)
	if err != nil {
		if os.Getenv("ALLOW_UNSUPPORTED") == "true" {
			log.Printf("unsupported machine %q, using fallback",
				cfg.machine)
			topo = mocktopo.NewFallback(8, "NVIDIA A100-SXM4-40GB")
		} else {
			return fmt.Errorf("failed to create topology: %w", err)
		}
	}

	gpuCount := len(topo.GPUs)
	modes := strings.Split(cfg.mode, ",")

	// Mode: fs (Step 1 behavior - proc/dev mock under -base)
	if contains(modes, "fs") || contains(modes, "all") {
		if err := runFS(cfg.base, topo); err != nil {
			return err
		}
	}

	// Mode: cdi (Step 2 - mock driver tree + CDI spec)
	if contains(modes, "cdi") || contains(modes, "all") {
		if err := runCDI(cfg, topo, gpuCount); err != nil {
			return err
		}
	}

	return nil
}

func runFS(base string, topo *mocktopo.Topology) error {
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

	log.Printf("mock filesystem (fs mode) written under %s (%d GPUs)\n",
		layout.Base, len(layout.GPUs))
	return nil
}

func runCDI(cfg *config, topo *mocktopo.Topology, gpuCount int) error {
	// Create mock driver tree
	files := mockdriver.DefaultFiles(cfg.driverRoot)
	if err := mockdriver.WriteAll(files); err != nil {
		return fmt.Errorf("failed to write driver files: %w", err)
	}
	log.Printf("mock driver tree written to %s\n", cfg.driverRoot)

	// Create device nodes (both host /dev and under driverRoot/dev)
	// Host /dev nodes for CDI runtime compatibility
	hostDevNodes := mockdriver.DeviceNodes("/dev", gpuCount, cfg.withDRI)
	if err := mockdriver.WriteAll(hostDevNodes); err != nil {
		log.Printf("warning: failed to create host /dev nodes: %v", err)
	}

	// Also create under driverRoot/dev for completeness
	driverDevNodes := mockdriver.DeviceNodes(cfg.driverRoot, gpuCount,
		cfg.withDRI)
	if err := mockdriver.WriteAll(driverDevNodes); err != nil {
		log.Printf("warning: failed to create %s/dev nodes: %v",
			cfg.driverRoot, err)
	}

	// Get the mock NVML library for CDI generation
	// This uses the same mock topology we're already using
	mockNVML, err := mocktopo.New(cfg.machine)
	if err != nil {
		if os.Getenv("ALLOW_UNSUPPORTED") == "true" {
			log.Printf("warning: using fallback mock for CDI generation")
			mockNVML = mocktopo.NewFallback(8, "NVIDIA A100-SXM4-40GB")
		} else {
			return fmt.Errorf("failed to create mock NVML: %w", err)
		}
	}

	// Generate CDI spec using nvidia-container-toolkit nvcdi library
	cdiOpts := cdi.Options{
		NVMLLib:           mockNVML.NVMLInterface(),
		DriverRoot:        cfg.driverRoot,
		DevRoot:           "/host/dev", // DevRoot is already prefixed by the DaemonSet mount
		NVIDIACDIHookPath: cfg.toolkitRoot + "/bin/nvidia-cdi-hook",
	}

	specYAML, err := cdi.Generate(cdiOpts)
	if err != nil {
		return fmt.Errorf("failed to generate CDI spec: %w", err)
	}

	// Validate before writing
	if err := cdi.Validate(specYAML); err != nil {
		return fmt.Errorf("CDI spec validation failed: %w", err)
	}

	// Write CDI spec
	if err := os.MkdirAll(filepath.Dir(cfg.cdiOutput), 0o755); err != nil {
		return fmt.Errorf("failed to create CDI directory: %w", err)
	}

	if err := os.WriteFile(cfg.cdiOutput, specYAML, 0o644); err != nil {
		return fmt.Errorf("failed to write CDI spec: %w", err)
	}

	log.Printf("CDI spec written to %s (generated via nvidia-container-toolkit)\n",
		cfg.cdiOutput)
	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
