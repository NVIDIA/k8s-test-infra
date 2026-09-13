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
	"io"
	"strconv"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mockctl"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

// The commands that operate on the override document as a whole: write
// arbitrary fields, report what is active, or clear it.

func setCommand() *cli.Command {
	return &cli.Command{
		Name:      "set",
		Usage:     "write arbitrary schema fields, as key.path=value",
		Flags:     []cli.Flag{gpuFlag()},
		Arguments: []cli.Argument{requiredStringArgs("key.path=value")},
		Action: func(_ context.Context, cmd *cli.Command) error {
			kv, err := mockctl.ParseSet(cmd.StringArgs("key.path=value"))
			if err != nil {
				return err
			}
			return applyPatch(cmd, func(*engine.DeviceConfig) map[string]any { return kv })
		},
	}
}

func resetCommand() *cli.Command {
	return &cli.Command{
		Name:  "reset",
		Usage: "clear the targeted overrides, returning the device(s) to the pristine profile",
		Flags: []cli.Flag{optionalGPUFlag()},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return mutate(cmd, func(doc *mockctl.Doc, target mockctl.Target, _ *engine.DeviceConfig) error {
				doc.Reset(target)
				return nil
			})
		},
	}
}

func statusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "print the overrides currently in effect",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "gpu",
				Usage: "report one device index (default: every override)",
			},
		},
		Action: runStatus,
	}
}

func runStatus(_ context.Context, cmd *cli.Command) error {
	doc, err := mockctl.Load(configOverridePath(cmd))
	if err != nil {
		return failf("load: %v", err)
	}
	out := cmd.Root().Writer

	if cmd.IsSet("gpu") {
		return printDeviceStatus(out, doc, cmd.Int("gpu"))
	}
	if doc.All == nil && len(doc.Devices) == 0 {
		fprintln(out, "no active overrides")
		return nil
	}
	return printDoc(out, doc)
}

// printDeviceStatus reports one device's bucket alongside the shared "all"
// bucket, which applies to that device too.
func printDeviceStatus(out io.Writer, doc *mockctl.Doc, index int) error {
	key := strconv.Itoa(index)
	device := doc.Devices[key]
	if device == nil && doc.All == nil {
		fprintf(out, "no active overrides for gpu %d\n", index)
		return nil
	}
	filtered := &mockctl.Doc{Version: doc.Version, All: doc.All}
	if device != nil {
		filtered.Devices = map[string]map[string]any{key: device}
	}
	return printDoc(out, filtered)
}

func printDoc(out io.Writer, doc *mockctl.Doc) error {
	b, err := doc.Bytes()
	if err != nil {
		return failf("%v", err)
	}
	fprint(out, string(b))
	return nil
}
