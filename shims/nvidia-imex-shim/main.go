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

// nvidia-imex-shim is a drop-in argv wrapper for the real `nvidia-imex`
// daemon. The upstream compute-domain-daemon hard-codes the daemon
// command line (`nvidia-imex -c /imexd/imexd.cfg`) with no
// flag passthrough, so GPU-less environments install this shim as
// nvidia-imex and the real binary beside it as nvidia-imex.real: the shim exec's the real daemon with
// `--nogpu` (NO GPU mode) appended, preserving all caller arguments,
// environment, stdio, and the process image (exec replaces the shim —
// no wrapper process lingers, signals reach the daemon directly).
// Remove once upstream supports passing extra IMEX daemon args. See
// NVIDIA/k8s-test-infra#304.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// envRealBin overrides the real binary location; used by tests and as
// an escape hatch for non-standard installs.
const envRealBin = "IMEX_SHIM_REAL_BIN"

// realBin returns the path of the real nvidia-imex binary. Resolving it beside
// the shim keeps the pair relocatable when a node driver root is mounted into a
// container at a different path.
func realBin(shimPath string) string {
	if v := os.Getenv(envRealBin); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(shimPath), "nvidia-imex.real")
}

// buildArgv assembles the exec argv: argv[0] is the real binary path,
// caller args follow unchanged, and --nogpu is appended unless the
// caller already passed it (either spelling the daemon accepts).
func buildArgv(realPath string, args []string) []string {
	argv := append([]string{realPath}, args...)
	for _, a := range args {
		if a == "--nogpu" || a == "-nogpu" {
			return argv
		}
	}
	return append(argv, "--nogpu")
}

func main() {
	shimPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nvidia-imex-shim: resolve executable: %v\n", err)
		os.Exit(127)
	}
	bin := realBin(shimPath)
	if err := syscall.Exec(bin, buildArgv(bin, os.Args[1:]), os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "nvidia-imex-shim: exec %s: %v\n", bin, err)
		os.Exit(127)
	}
}
