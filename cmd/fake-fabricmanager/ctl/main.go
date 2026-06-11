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

// fake-fabricmanager-ctl is a tiny readiness probe for the fake
// fabricmanager daemon. With -q it prints "READY\n" and exits 0 iff the
// daemon's node-local readiness marker exists under the shared state
// directory; otherwise it exits 1. Modeled on cmd/fake-imex/ctl.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/NVIDIA/k8s-test-infra/pkg/fmcoord"
)

func main() {
	cfg := flag.String("c", "", "fabricmanager config file (ignored)")
	query := flag.Bool("q", false, "query readiness (only supported mode)")
	flag.Parse()
	_ = cfg

	if !*query {
		fmt.Fprintln(os.Stderr, "fake-fabricmanager-ctl: only -q (query) mode is supported")
		os.Exit(2)
	}

	if !fmcoord.IsReady(fmcoord.StateDir()) {
		fmt.Fprintln(os.Stderr, "fake-fabricmanager-ctl: fabric manager not ready")
		os.Exit(1)
	}
	fmt.Print("READY\n")
}
