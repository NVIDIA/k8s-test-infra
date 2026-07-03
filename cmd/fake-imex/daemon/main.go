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

// fake-imex-daemon is a drop-in replacement for the real `nvidia-imex`
// binary used inside the compute-domain-daemon pod. It accepts (and
// ignores) the standard CLI flags, writes a marker file under the
// shared hostPath state directory, and re-reads nodes.cfg on SIGUSR1
// (matching the upstream daemon's DNS-update signal). See
// NVIDIA/k8s-test-infra#304 for the full ComputeDomain simulation
// design.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/imexcoord" //nolint:staticcheck // the fakes ARE the deprecated subsystem; removed together with imexcoord (#304)
)

func main() {
	// Accept the real binary's flags so callers (compute-domain-daemon's
	// subprocess invocation) don't have to special-case the mock.
	cfg := flag.String("c", "", "imexd config file (ignored)")
	flag.Parse()
	_ = cfg

	log.Printf("fake-imex: DEPRECATED — this fake nvidia-imex will be removed " +
		"in a follow-up release; the ComputeDomain simulation now runs the real " +
		"nvidia-imex daemon in NO GPU mode (--nogpu) via imex-nogpu-shim. See " +
		"NVIDIA/k8s-test-infra#304.")

	podIP := os.Getenv(imexcoord.EnvPodIP)
	if podIP == "" {
		log.Fatalf("fake-imex: %s is required (set via downward API)", imexcoord.EnvPodIP)
	}

	stateDir := imexcoord.StateDir()
	nodesCfg := imexcoord.NodesConfigPath()

	if err := imexcoord.WriteMarker(stateDir, podIP); err != nil {
		log.Fatalf("fake-imex: write marker: %v", err)
	}
	log.Printf("fake-imex: marker %s written; tracking peers via %s", podIP, nodesCfg)

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	logPeers := func(reason string) {
		peers, err := imexcoord.ReadPeers(nodesCfg)
		if err != nil {
			log.Printf("fake-imex: re-read nodes.cfg (%s) failed: %v", reason, err)
			return
		}
		log.Printf("fake-imex: nodes.cfg=%s peers=%d (%s)", nodesCfg, len(peers), reason)
	}
	logPeers("startup")

	for {
		select {
		case sig := <-sigs:
			switch sig {
			case syscall.SIGUSR1:
				logPeers("SIGUSR1")
			case syscall.SIGTERM, syscall.SIGINT:
				if err := imexcoord.RemoveMarker(stateDir, podIP); err != nil {
					log.Printf("fake-imex: remove marker on shutdown: %v", err)
				}
				log.Printf("fake-imex: marker %s removed; exiting on %s", podIP, sig)
				_, _ = fmt.Fprintln(os.Stdout, "fake-imex: clean shutdown")
				return
			}
		case <-tick.C:
			// Re-assert the marker on every tick so the daemon
			// self-heals when the marker is externally deleted
			// (hostPath GC, accidental cleanup, volume remount).
			// WriteMarker is idempotent so this is cheap.
			if err := imexcoord.WriteMarker(stateDir, podIP); err != nil {
				log.Printf("fake-imex: re-assert marker on tick: %v", err)
			}
			logPeers("tick")
		}
	}
}
