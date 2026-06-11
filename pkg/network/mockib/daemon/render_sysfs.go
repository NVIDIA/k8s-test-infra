// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
)

// RenderSysfsOptions configures profile-driven sysfs rendering.
type RenderSysfsOptions struct {
	ConfigPath string
	GPUCount   int
	NodeName   string
	OutputDir  string
	DryRun     bool
}

// RenderSysfsFromConfig reads a mock-nvml profile YAML and writes the fake IB
// sysfs tree under OutputDir when infiniband.enabled is true.
func RenderSysfsFromConfig(opts RenderSysfsOptions) error {
	if opts.ConfigPath == "" {
		return fmt.Errorf("config path required")
	}
	if opts.OutputDir == "" {
		return fmt.Errorf("output directory required")
	}
	data, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var prof config.Profile
	if err := yaml.Unmarshal(data, &prof); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if !prof.Infiniband.Enabled {
		return nil
	}
	if opts.DryRun {
		return nil
	}
	return render.Render(render.Options{
		IB:       prof.Infiniband,
		GPUCount: opts.GPUCount,
		NodeName: opts.NodeName,
		Output:   opts.OutputDir,
	})
}
