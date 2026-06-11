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

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Fabric-state coupling (decision D-a). A GPU whose configured fabric
// state is "auto" mirrors the fake fabricmanager's readiness:
//
//   - Coupling is OFF unless EnvFabricStateDir is set (fabricmanager
//     enabled, wired by setup.sh / the Helm DaemonSet). With coupling off,
//     "auto" resolves to COMPLETED so single-node and ComputeDomain
//     behavior (#304/#342) is unchanged — there is no regression when
//     fabricmanager is disabled.
//   - With coupling on, "auto" is COMPLETED only once the fabricmanager
//     daemon has written its readiness marker, otherwise IN_PROGRESS.
//
// The marker is read through a short-TTL cache so the hot NVML getters
// never stat() the filesystem per call (the trap BALTHASAR flagged on
// CASPER's original design).
const (
	// EnvFabricStateDir activates fabric-state coupling and locates the
	// fabricmanager readiness marker directory.
	EnvFabricStateDir = "MOCK_FABRICMANAGER_STATE_DIR"

	// FabricReadyMarker is the node-local readiness file the fake
	// fabricmanager daemon writes on startup and removes on shutdown.
	FabricReadyMarker = "fabricmanager.ready"

	// fabricReadinessTTL bounds how often the marker is stat()'d.
	fabricReadinessTTL = time.Second
)

type fabricReadinessCache struct {
	mu      sync.Mutex
	checked time.Time
	ready   bool

	// now and stat are injectable for tests.
	now  func() time.Time
	stat func(string) (os.FileInfo, error)
}

var fabricReadiness = &fabricReadinessCache{now: time.Now, stat: os.Stat}

// state resolves the registration state for a GPU configured with fabric
// state "auto". COMPLETED when coupling is inactive (fabricmanager
// disabled); otherwise it tracks the readiness marker.
func (c *fabricReadinessCache) state() uint8 {
	dir := strings.TrimSpace(os.Getenv(EnvFabricStateDir))
	if dir == "" {
		return FabricStateCompleted
	}
	if c.isReady(filepath.Join(dir, FabricReadyMarker)) {
		return FabricStateCompleted
	}
	return FabricStateInProgress
}

func (c *fabricReadinessCache) isReady(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if !c.checked.IsZero() && now.Sub(c.checked) < fabricReadinessTTL {
		return c.ready
	}
	_, err := c.stat(path)
	c.ready = err == nil
	c.checked = now
	return c.ready
}
