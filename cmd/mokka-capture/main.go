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
// committed, and compares it with what Mokka serves. It runs on the developer's
// machine and reaches the nodes through kubectl. See
// docs/contributing/capturing-a-real-node.md.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/collect"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/nvmldump"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/scrub"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/smidiff"
)

const usage = `Usage: mokka-capture <command> [args]

Commands:
  collect    capture nodes and the cluster through kubectl
  scrub      replace identifiers in a capture, or -check that none are left
  diff smi   compare two nvidia-smi -q -x documents
  diff nvml  compare two NVML recordings
  dump       record the NVML library on the loader path (collect runs this
             in the driver container)

Run mokka-capture <command> -h for a command's flags.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "collect":
		return collect.Run(rest, stdout, stderr)
	case "scrub":
		return scrub.Run(rest, stdout, stderr)
	case "dump":
		return nvmldump.Dump(rest, stdout, stderr)
	case "diff":
		if len(rest) > 0 && rest[0] == "smi" {
			return smidiff.Run(rest[1:], stdout, stderr)
		}
		if len(rest) > 0 && rest[0] == "nvml" {
			return nvmldump.Diff(rest[1:], stdout, stderr)
		}
		_, _ = io.WriteString(stderr, "usage: mokka-capture diff smi|nvml [flags] real mock\n")
		return 2
	case "-h", "--help", "help":
		_, _ = io.WriteString(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}
