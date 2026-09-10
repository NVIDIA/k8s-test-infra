// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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
	"errors"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// gpuFlag declares the device selector every mutating command needs.
//
// Required only rejects an absent flag, not an empty one, and mutate reads an
// empty target as the shared "all" bucket — so without the validator a
// mistyped `--gpu ""` would quietly mutate every device on the node.
func gpuFlag() cli.Flag {
	return &cli.StringFlag{
		Name:     "gpu",
		Usage:    "target device: an index, 'all', or a GPU UUID",
		Required: true,
		Validator: func(spec string) error {
			if strings.TrimSpace(spec) == "" {
				return errors.New("--gpu needs a target: an index, 'all', or a GPU UUID")
			}
			return nil
		},
	}
}

// optionalGPUFlag is gpuFlag for reset, where omitting the target clears every
// bucket rather than being a usage error.
func optionalGPUFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "gpu",
		Usage: "target device: an index, 'all', or a GPU UUID (default: every device)",
	}
}

// configOverridePath and configPath keep the CLI's "set but empty means unset"
// rule: an exported-and-empty MOCK_NVML_OVERRIDES or MOCK_NVML_CONFIG must fall
// back to the default rather than resolve to the empty path.
func configOverridePath(cmd *cli.Command) string {
	return pathOr(cmd.String("file"), defaultConfigOverride)
}

func configPath(cmd *cli.Command) string {
	return pathOr(cmd.String("config"), defaultConfig)
}

func pathOr(path, def string) string {
	if path == "" {
		return def
	}
	return path
}

// mutation applies one command's change to the override document. It runs with
// the file lock held, between the load and the atomic write.
type mutation func(doc *mockctl.Doc, target mockctl.Target, base *engine.DeviceConfig) error

// mutate is the read-modify-write every mutating command shares. The whole
// resulting document is validated, not just the patch, so a bad value in any
// bucket fails the command instead of reaching a consumer process.
func mutate(cmd *cli.Command, apply mutation) error {
	return mutateWithConfig(cmd, loadConfig(cmd), apply)
}

// mutateWithConfig is mutate for a command that already needs the pristine
// profile for its own checks, so the load — and its warning when the profile is
// unreadable — happens once per invocation rather than twice.
func mutateWithConfig(cmd *cli.Command, cfg *engine.Config, apply mutation) error {
	path := configOverridePath(cmd)
	base := deviceDefaults(cfg)

	spec := cmd.String("gpu")
	target := mockctl.Target{All: true} // no --gpu (reset only) means everything
	if spec != "" {
		var err error
		if target, err = mockctl.ResolveTarget(spec, cfg); err != nil {
			return err
		}
	}

	unlock, err := mockctl.LockOverride(path)
	if err != nil {
		return failf("lock: %v", err)
	}
	defer unlock()

	doc, err := mockctl.Load(path)
	if err != nil {
		return failf("load: %v", err)
	}

	if err := apply(doc, target, base); err != nil {
		return err
	}
	if err := validateDoc(doc, base); err != nil {
		return fmt.Errorf("invalid config override: %w", err)
	}
	if err := mockctl.WriteAtomic(path, doc); err != nil {
		return failf("write: %v", err)
	}

	fprintf(cmd.Root().Writer, "ok: %s applied to %s\n", cmd.Name, gpuLabel(spec))
	return nil
}

// applyPatch is mutate for a command whose patch depends on the pristine
// profile: it validates the patch against the device schema before merging it
// into the target bucket.
func applyPatch(cmd *cli.Command, build func(base *engine.DeviceConfig) map[string]any) error {
	return mutate(cmd, func(doc *mockctl.Doc, target mockctl.Target, base *engine.DeviceConfig) error {
		patch := build(base)
		if err := mockctl.Validate(base, patch); err != nil {
			return fmt.Errorf("invalid: %w", err)
		}
		doc.SetFields(target, patch)
		return nil
	})
}

// setPatch is applyPatch for a patch the command already holds.
func setPatch(cmd *cli.Command, patch map[string]any) error {
	return applyPatch(cmd, func(*engine.DeviceConfig) map[string]any { return patch })
}

// validateDoc runs the schema merge for the shared bucket and every per-device
// bucket, so a bad value anywhere fails the command.
func validateDoc(doc *mockctl.Doc, base *engine.DeviceConfig) error {
	if doc.All != nil {
		if err := mockctl.Validate(base, doc.All); err != nil {
			return fmt.Errorf("all: %w", err)
		}
	}
	for idx, patch := range doc.Devices {
		if err := mockctl.Validate(base, patch); err != nil {
			return fmt.Errorf("device %s: %w", idx, err)
		}
	}
	return nil
}

// loadConfig loads the pristine profile so `--gpu` can resolve UUIDs and the
// index bounds check knows the real device count. NumDevices is resolved the
// same way the engine's LoadConfig does: the device-list length, unless
// system.num_devices (which the node agent stamps from GPU_COUNT) overrides it — so
// the bounds check matches the count the running engine actually serves even
// when gpu.count differs from the profile's device list. A load failure is
// non-fatal (UUID resolution and the bounds check degrade to best-effort) but
// is surfaced as a warning instead of being swallowed silently.
func loadConfig(cmd *cli.Command) *engine.Config {
	path := configPath(cmd)
	yc, err := engine.LoadYAMLConfig(path)
	if err != nil {
		fprintf(cmd.Root().ErrWriter,
			"warning: could not load config %q: %v; UUID resolution and --gpu bounds checks are disabled\n", path, err)
		return nil
	}
	numDevices := len(yc.Devices)
	if numDevices == 0 {
		numDevices = 8 // engine default when no device list is present
	}
	if yc.System.NumDevices > 0 {
		numDevices = yc.System.NumDevices // system.num_devices wins, matching the engine
	}
	return &engine.Config{YAMLConfig: yc, NumDevices: numDevices, DriverVersion: yc.System.DriverVersion}
}

func deviceDefaults(cfg *engine.Config) *engine.DeviceConfig {
	if cfg == nil || cfg.YAMLConfig == nil {
		return &engine.DeviceConfig{}
	}
	dd := cfg.YAMLConfig.DeviceDefaults
	return &dd
}

// baseFanCount reports the fan count declared by the base config so the fan
// command can preserve a multi-fan count instead of collapsing it to 1.
func baseFanCount(base *engine.DeviceConfig) int {
	if base != nil && base.Fan != nil {
		return base.Fan.Count
	}
	return 0
}

func gpuLabel(spec string) string {
	if spec == "" {
		return "all"
	}
	return spec
}
