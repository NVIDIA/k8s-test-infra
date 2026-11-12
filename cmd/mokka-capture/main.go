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

// mokka-capture captures a real GPU node, scrubs the capture so it can be
// committed, writes a mock profile from it, and compares it with what Mokka
// serves. It runs on the developer's
// machine and reaches the nodes through kubectl. See
// docs/contributing/capturing-a-real-node.md.
package main

import (
	"context"
	"io"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/collect"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/nvmldump"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/profile"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/scrub"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/smidiff"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one invocation and returns its exit code. Keeping the process
// exit in main alone lets a test drive the command line end to end.
func run(args []string, stdout, stderr io.Writer) int {
	return cmdline.Run(context.Background(), newCLI(), args, stdout, stderr)
}

func newCLI() *cli.Command {
	return &cli.Command{
		Name:  "mokka-capture",
		Usage: "capture a real GPU node, scrub the capture, write a mock profile from it, and compare it with what Mokka serves",
		Description: "Runs on your machine and reaches the nodes through kubectl. " +
			"The procedure is in docs/contributing/capturing-a-real-node.md.",
		Commands: []*cli.Command{
			collect.Command(),
			scrub.Command(),
			profile.Command(),
			{
				Name:     "diff",
				Usage:    "compare a real node's nvidia-smi document or NVML recording with the mock's",
				Commands: []*cli.Command{smidiff.Command(), nvmldump.DiffCommand()},
				Action:   cmdline.UnknownCommand,
			},
			nvmldump.DumpCommand(),
		},
		Action: cmdline.UnknownCommand,
	}
}
