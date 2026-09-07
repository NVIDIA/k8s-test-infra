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

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// The commands that pin one reported reading. Each parses its own positional
// value and leaves the schema patch to pkg/gpu/mockctl.

// maxPowerWatts bounds the `power` command. Real GPUs top out around ~1.5kW;
// this generous cap keeps watts*1000 far under the uint32 milliwatt ceiling
// (~4.29e6 W) so the schema-field conversion can never overflow.
const maxPowerWatts = 100000

func temperatureCommand() *cli.Command {
	return &cli.Command{
		Name:      "temp",
		Aliases:   []string{"temperature"},
		Usage:     "pin reported GPU temperature",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredIntArg("celsius")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			celsius, err := intArg(cmd, "celsius", 0, 200)
			if err != nil {
				return err
			}
			return setPatch(cmd, mockctl.TemperaturePatch(celsius))
		},
	}
}

func powerCommand() *cli.Command {
	return &cli.Command{
		Name:      "power",
		Usage:     "pin reported power draw",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredFloatArg("watts")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			watts, err := floatArg(cmd, "watts", 0, maxPowerWatts)
			if err != nil {
				return err
			}
			// NVML power fields are milliwatts; the CLI takes the watts value
			// nvidia-smi displays and converts.
			return setPatch(cmd, mockctl.PowerPatch(uint32(watts*1000+0.5)))
		},
	}
}

func fanCommand() *cli.Command {
	return &cli.Command{
		Name:      "fan",
		Usage:     "pin reported fan speed (forces fan count >= 1)",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredIntArg("percent")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			percent, err := intArg(cmd, "percent", 0, 100)
			if err != nil {
				return err
			}
			return applyPatch(cmd, func(base *engine.DeviceConfig) map[string]any {
				return mockctl.FanPatch(percent, baseFanCount(base))
			})
		},
	}
}

func utilizationCommand() *cli.Command {
	return &cli.Command{
		Name:      "util",
		Aliases:   []string{"utilization"},
		Usage:     "pin reported GPU + memory utilization",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredIntArg("percent")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			percent, err := intArg(cmd, "percent", 0, 100)
			if err != nil {
				return err
			}
			return setPatch(cmd, mockctl.UtilizationPatch(percent))
		},
	}
}

func clocksCommand() *cli.Command {
	return &cli.Command{
		Name:      "clocks",
		Usage:     "pin reported SM + graphics clocks",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredIntArg("mhz")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			mhz, err := intArg(cmd, "mhz", 0, 100000)
			if err != nil {
				return err
			}
			return setPatch(cmd, mockctl.ClocksPatch(mhz))
		},
	}
}

func throttleCommand() *cli.Command {
	return &cli.Command{
		Name:      "throttle",
		Usage:     "set the active throttle reasons ('none' clears them)",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredStringArgs("reason")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			patch, err := mockctl.ThrottlePatch(cmd.StringArgs("reason"))
			if err != nil {
				return err
			}
			return setPatch(cmd, patch)
		},
	}
}

func pstateCommand() *cli.Command {
	return &cli.Command{
		Name:      "pstate",
		Usage:     "pin the reported performance state (P-state)",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredIntArg("pstate")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			n, err := intArg(cmd, "pstate", 0, 15)
			if err != nil {
				return err
			}
			return setPatch(cmd, mockctl.PStatePatch(n))
		},
	}
}
