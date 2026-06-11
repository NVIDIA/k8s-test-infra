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

// fake-fabricmanager-daemon is a drop-in stand-in for the real
// `nv-fabricmanager` service used on NVSwitch (HGX / GB200) platforms. It
// accepts (and ignores) the real binary's flags, optionally waits a short
// "registration" delay, then writes a node-local readiness marker under the
// shared state directory and re-asserts it on a 2s tick. On SIGTERM/SIGINT
// it removes the marker and exits cleanly.
//
// The mock NVML engine reads the same marker to resolve a GPU's fabric
// state when it is configured as "auto" (see
// pkg/gpu/mocknvml/engine/fabric_readiness.go), so enabling this daemon
// flips GB200/H100-HGX GPUs from IN_PROGRESS to COMPLETED — mirroring how a
// real fabricmanager gates GPU readiness. See NVIDIA/k8s-test-infra#371.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/fmcoord"
)

// envInitDelay optionally delays the readiness marker write to simulate
// fabric registration latency, exercising the IN_PROGRESS -> COMPLETED
// transition the mock NVML engine surfaces.
const envInitDelay = "MOCK_FABRICMANAGER_INIT_DELAY_SEC"

func main() {
	// Accept the real binary's common flags so callers don't special-case
	// the mock.
	cfg := flag.String("c", "", "fabricmanager config file (ignored)")
	flag.Parse()
	_ = cfg

	stateDir := fmcoord.StateDir()

	if d := initDelay(); d > 0 {
		log.Printf("fake-fabricmanager: simulating %s registration delay", d)
		time.Sleep(d)
	}

	if err := fmcoord.WriteReady(stateDir); err != nil {
		log.Fatalf("fake-fabricmanager: write readiness marker: %v", err)
	}
	log.Printf("fake-fabricmanager: ready marker %s written", fmcoord.MarkerPath(stateDir))

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case sig := <-sigs:
			if err := fmcoord.RemoveReady(stateDir); err != nil {
				log.Printf("fake-fabricmanager: remove marker on shutdown: %v", err)
			}
			log.Printf("fake-fabricmanager: marker removed; exiting on %s", sig)
			_, _ = fmt.Fprintln(os.Stdout, "fake-fabricmanager: clean shutdown")
			return
		case <-tick.C:
			// Re-assert the marker so the daemon self-heals if the marker
			// is externally deleted (hostPath GC, accidental cleanup).
			// WriteReady is idempotent so this is cheap.
			if err := fmcoord.WriteReady(stateDir); err != nil {
				log.Printf("fake-fabricmanager: re-assert marker on tick: %v", err)
			}
		}
	}
}

func initDelay() time.Duration {
	v := os.Getenv(envInitDelay)
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
