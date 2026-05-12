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

// fake-imex-ctl is a drop-in replacement for `nvidia-imex-ctl` used by
// the compute-domain-daemon's readiness probe. It supports the
// `-c <config> -q` invocation pattern from upstream's check() helper:
// prints "READY\n" + exit 0 iff every peer listed in nodes.cfg has a
// marker file under the shared state directory; otherwise exits 1.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/NVIDIA/k8s-test-infra/pkg/imexcoord"
)

func main() {
	cfg := flag.String("c", "", "imexd config file (ignored)")
	query := flag.Bool("q", false, "query readiness (only supported mode)")
	flag.Parse()
	_ = cfg

	if !*query {
		fmt.Fprintln(os.Stderr, "fake-imex-ctl: only -q (query) mode is supported")
		os.Exit(2)
	}

	peers, err := imexcoord.ReadPeers(imexcoord.NodesConfigPath())
	if err != nil {
		// Match real nvidia-imex-ctl behaviour: when the daemon has not
		// yet written nodes.cfg the probe should fail rather than say
		// READY. Avoids a race where the controller marks a domain
		// ready before its members have actually started.
		fmt.Fprintf(os.Stderr, "fake-imex-ctl: read nodes.cfg: %v\n", err)
		os.Exit(1)
	}

	ready, missing := imexcoord.AllPeersReady(imexcoord.StateDir(), peers)
	if !ready {
		fmt.Fprintf(os.Stderr, "fake-imex-ctl: peer %s not ready\n", missing)
		os.Exit(1)
	}
	fmt.Print("READY\n")
}
