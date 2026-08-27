// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package fabricmanager defines the marker-file protocol that stands in for
// NVSwitch fabric registration. The node agent's fabricmanager simulator writes
// the marker; the mock NVML engine reads it to decide whether a GPU configured
// with `fabric.state: auto` reports IN_PROGRESS or COMPLETED.
//
// A single node-local file suffices because the real fabric manager is itself
// node-local: it manages the NVSwitches on its own node, so there is no
// multi-peer state to agree on.
//
// The engine does not import this package — it stats the marker behind a TTL
// cache because it sits on the NVML call path. EnvStateDir and ReadyMarker are
// therefore the canonical definitions its copies are checked against; see
// engine's fabricmanager contract test.
package fabricmanager

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultStateDir is the directory the chart configures by default. It lives
// under the mock root so the agent reaches it through the hostPath it already
// mounts, and workloads through the bind the CDI spec adds.
const DefaultStateDir = "/var/lib/nvml-mock/fabric-state"

// ReadyMarker is the readiness file name. Must equal engine.FabricReadyMarker.
const ReadyMarker = "fabricmanager.ready"

// EnvStateDir carries the state directory to every process that needs it.
// Must equal engine.EnvFabricStateDir.
const EnvStateDir = "MOCK_FABRICMANAGER_STATE_DIR"

// MarkerPath returns the readiness marker path under stateDir.
func MarkerPath(stateDir string) string {
	return filepath.Join(stateDir, ReadyMarker)
}

// WriteReady publishes readiness. Idempotent, because the simulator re-asserts
// the marker on a timer rather than writing it once.
func WriteReady(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("fabricmanager: mkdir %s: %w", stateDir, err)
	}

	path := MarkerPath(stateDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)

	if err != nil {
		return fmt.Errorf("fabricmanager: write marker %s: %w", path, err)
	}

	return f.Close()
}

// RemoveReady withdraws readiness. A missing marker is not an error: shutdown
// and stale-marker cleanup both run against state that may be partial.
func RemoveReady(stateDir string) error {
	if err := os.Remove(MarkerPath(stateDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fabricmanager: remove marker: %w", err)
	}
	return nil
}
