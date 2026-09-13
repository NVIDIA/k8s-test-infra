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

// The commands that inject a fault. Each is authoritative for the block it
// writes, so re-running one replaces the previous injection instead of
// accumulating on top of it, and the healing value ('healthy', 'none', 0)
// clears just that block.

const (
	// maxNvlinkErrorRate bounds the `nvlink-error` command (errors/second). A
	// degrading NVLink tops out well below this; the generous cap keeps the
	// accrual math (rate * elapsed seconds) far from overflowing uint64.
	maxNvlinkErrorRate = 1e9

	// maxSramEccCount bounds the `sram-ecc` command. A GPU is retired long
	// before its SRAM error count approaches this, so the cap only exists to
	// keep an obvious typo from being written as a plausible counter.
	maxSramEccCount = 1e9
)

func failCommand() *cli.Command {
	return &cli.Command{
		Name:  "fail",
		Usage: "inject a device failure, or clear one with --mode healthy",
		Flags: []cli.Flag{
			gpuFlag(),
			&cli.StringFlag{
				Name:     "mode",
				Usage:    "failure mode: healthy | lost | fallen_off_bus | ecc_uncorrectable",
				Required: true,
			},
			&cli.IntFlag{
				Name:  "after-calls",
				Usage: "trip only after N guarded calls have succeeded",
			},
			// The node agent watches the override this writes and announces
			// the Xid on the node's kernel log, as a driver's printk does.
			&cli.Uint64Flag{
				Name:  "xid",
				Usage: "Xid code to surface alongside the failure",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return mutate(cmd, func(doc *mockctl.Doc, target mockctl.Target, _ *engine.DeviceConfig) error {
				return doc.Fail(target, cmd.String("mode"), cmd.Int("after-calls"), cmd.Uint64("xid"))
			})
		},
	}
}

func nvlinkErrorCommand() *cli.Command {
	return &cli.Command{
		Name:  "nvlink-error",
		Usage: "inject NVLink DL errors at a rate in errors/second (0 heals)",
		Flags: []cli.Flag{
			gpuFlag(),
			&cli.StringFlag{
				Name:  "links",
				Usage: "comma-separated NVLink ids to inject on (default: every active link)",
			},
		},
		Arguments: []cli.Argument{requiredFloatArg("errors_per_sec")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			rate, err := floatArg(cmd, "errors_per_sec", 0, maxNvlinkErrorRate)
			if err != nil {
				return err
			}
			links, err := parseLinkIDs(cmd.String("links"))
			if err != nil {
				return err
			}
			return setPatch(cmd, mockctl.NVLinkErrorPatch(rate, links))
		},
	}
}

func sramECCCommand() *cli.Command {
	return &cli.Command{
		Name:  "sram-ecc",
		Usage: "inject SRAM ECC errors (0 heals)",
		Flags: []cli.Flag{
			gpuFlag(),
			&cli.StringFlag{
				Name:  "type",
				Value: "secded",
				Usage: "SRAM error type: correctable | parity | secded",
			},
			&cli.StringFlag{
				Name:  "source",
				Usage: "unit the errors are attributed to: l2 | sm | microcontroller | pcie | other",
			},
			&cli.BoolFlag{
				Name:  "threshold-exceeded",
				Usage: "raise the SRAM error threshold flag",
			},
		},
		Arguments: []cli.Argument{requiredIntArg("count")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			count, err := intArg(cmd, "count", 0, maxSramEccCount)
			if err != nil {
				return err
			}
			patch, err := mockctl.SramECCPatch(
				uint64(count), cmd.String("type"), cmd.String("source"), cmd.Bool("threshold-exceeded"))
			if err != nil {
				return err
			}
			return setPatch(cmd, patch)
		},
	}
}

func fabricHealthCommand() *cli.Command {
	return &cli.Command{
		Name:  "fabric-health",
		Usage: "degrade NVLink fabric health ('healthy' clears it)",
		Description: "conditions: degraded_bandwidth, route_recovery, route_unhealthy,\n" +
			"access_timeout_recovery, or a misconfiguration (no_partition,\n" +
			"insufficient_nvlinks, incompatible_gpu_fw, invalid_location,\n" +
			"incorrect_sysguid, incorrect_chassis_sn, gpu_state_invalid)",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredStringArgs("condition")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			patch, err := mockctl.FabricHealthPatch(cmd.StringArgs("condition"))
			if err != nil {
				return err
			}
			return setPatch(cmd, patch)
		},
	}
}

// parseLinkIDs parses the --links CSV (e.g. "0,3,7") into a sorted-as-given
// slice of non-negative link ids. An empty string yields nil (inject on all
// active links). Whitespace around entries is tolerated.
func parseLinkIDs(csv string) ([]int, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil, nil
	}
	var out []int
	for _, tok := range strings.Split(csv, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		v, err := strconv.Atoi(tok)
		if err != nil {
			return nil, fmt.Errorf("--links entry %q must be an integer", tok)
		}
		if v < 0 {
			return nil, fmt.Errorf("--links entry %d must be non-negative", v)
		}
		out = append(out, v)
	}
	return out, nil
}
