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
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// migCommand repartitions a running node's simulated GPUs.
//
// --gpu lives on the parent so it can precede the subcommand, which is what
// lets --profile and --count follow it: a flag cannot come after a positional
// argument, but it can come after a subcommand name.
func migCommand() *cli.Command {
	return &cli.Command{
		Name:  "mig",
		Usage: "enable, disable or re-lay-out MIG partitioning",
		Description: "Changes the NVML view only. The /dev/nvidia-caps nodes and the\n" +
			"mig-minors table stay as the node agent staged them from the profile,\n" +
			"so the device plugin cannot allocate partitions created this way.",
		Flags: []cli.Flag{gpuFlag()},
		Commands: []*cli.Command{
			migEnableCommand(),
			migDisableCommand(),
		},
		// Naming no verb changes nothing, so it is a usage error rather than a
		// command that exits cleanly having done nothing to the node.
		Action: usageAction,
	}
}

func migEnableCommand() *cli.Command {
	return &cli.Command{
		Name:  "enable",
		Usage: "turn MIG on, optionally with a layout (default: the profile's declared layout)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "profile",
				Usage: "GPU instance profile to fill the board with, e.g. 1g.5gb",
			},
			&cli.IntFlag{
				Name:  "count",
				Usage: "how many instances of --profile to create (default: 1)",
			},
			migForceFlag(),
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			mig, err := mockctl.MIGEnablePatch(cmd.String("profile"), cmd.Int("count"))
			if err != nil {
				return err
			}
			return applyMIG(cmd, mig)
		},
	}
}

func migDisableCommand() *cli.Command {
	return &cli.Command{
		Name:  "disable",
		Usage: "turn MIG off, destroying every partition",
		Flags: []cli.Flag{migForceFlag()},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return applyMIG(cmd, mockctl.MIGDisablePatch())
		},
	}
}

func migForceFlag() cli.Flag {
	return &cli.BoolFlag{
		Name:  "force",
		Usage: "apply the change even when the target declares compute processes",
	}
}

// applyMIG runs the three pre-write checks and then assigns the block. All
// three run before anything is written, so a refused command leaves the
// override document exactly as it was.
func applyMIG(cmd *cli.Command, mig map[string]any) error {
	cfg := loadConfig(cmd)
	return mutateWithConfig(cmd, cfg, func(doc *mockctl.Doc, target mockctl.Target, base *engine.DeviceConfig) error {
		patch := map[string]any{"mig": mig}
		if err := mockctl.Validate(base, patch); err != nil {
			return fmt.Errorf("invalid: %w", err)
		}
		if err := mockctl.ValidateMIGLayout(base, mig); err != nil {
			return err
		}
		// An unreadable profile leaves nothing to inspect for processes, so the
		// guard cannot fire and the write goes ahead. That matches how the rest
		// of the CLI degrades without a profile — the --gpu bounds check and the
		// layout check above are both best-effort in that state — and refusing
		// every mutation instead would leave an operator no way to change a
		// node whose profile went missing.
		if !cmd.Bool("force") {
			if busy := devicesWithProcesses(cfg, target); len(busy) > 0 {
				return failf(
					"%s. Changing MIG partitioning would strand that work; pass --force to do it anyway",
					inUseSummary(busy))
			}
		}
		doc.SetMIG(target, mig)
		return nil
	})
}

// inUseSummary names the busy devices, reading correctly for one device and
// for several.
func inUseSummary(busy []string) string {
	if len(busy) == 1 {
		return fmt.Sprintf("GPU %s is in use: it declares compute processes", busy[0])
	}
	return fmt.Sprintf("GPUs %s are in use: they declare compute processes", strings.Join(busy, ", "))
}

// devicesWithProcesses names the targeted devices that declare compute
// processes, which is the closest node-wide stand-in for a busy GPU: instances
// a consumer created through NVML live only in that consumer's process, so the
// CLI cannot see them, while declared processes are state both sides read. Only
// the pristine profile is read, not the effective document, so processes
// injected through this CLI's own `set` command do not arm the guard.
func devicesWithProcesses(cfg *engine.Config, target mockctl.Target) []string {
	if cfg == nil {
		return nil
	}
	var busy []string
	for idx := range cfg.NumDevices {
		if !target.All && idx != target.Index {
			continue
		}
		if dc := cfg.GetDeviceConfig(idx); dc != nil && len(dc.Processes) > 0 {
			busy = append(busy, strconv.Itoa(idx))
		}
	}
	return busy
}
