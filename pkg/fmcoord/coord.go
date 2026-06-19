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

// Package fmcoord implements the marker-file coordination protocol used by
// the fake nvidia-fabricmanager binaries (cmd/fake-fabricmanager/{daemon,ctl})
// to simulate fabric-manager readiness on KIND clusters without real NVSwitch
// hardware. It mirrors pkg/imexcoord, but fabric manager is a node-local
// service (it manages the NVSwitches on its own node), so readiness is a
// single node-local marker rather than a multi-peer set.
//
// Protocol:
//
//  1. The fake nvidia-fabricmanager daemon writes an empty marker file at
//     <stateDir>/fabricmanager.ready on startup and removes it on
//     SIGTERM/SIGINT.
//  2. The fake nvidia-fabricmanager-ctl readiness probe (and the mock NVML
//     engine's fabric-state coupling) report READY iff that marker exists.
//
// The same marker path is read by the mock NVML engine
// (pkg/gpu/mocknvml/engine/fabric_readiness.go) so a GPU configured with
// fabric state "auto" reports COMPLETED only once the daemon is ready.
package fmcoord

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultStateDir is the canonical hostPath shared between the nvml-mock
// pod (which runs the fake fabricmanager daemon and the mock NVML library)
// and any consumer. The Helm chart mounts it when fabricmanager.enabled.
const DefaultStateDir = "/var/lib/nvml-mock/fabric-state"

// ReadyMarker is the node-local readiness file name. It must match
// engine.FabricReadyMarker so the NVML fabric-state coupling reads the
// same path the daemon writes.
const ReadyMarker = "fabricmanager.ready"

// EnvStateDir overrides DefaultStateDir. The daemon, ctl, and the mock
// NVML engine all honour it so tests can run against a temp directory.
const EnvStateDir = "MOCK_FABRICMANAGER_STATE_DIR"

// StateDir returns the effective state directory, falling back to
// DefaultStateDir when EnvStateDir is empty.
func StateDir() string {
	if v := os.Getenv(EnvStateDir); v != "" {
		return v
	}
	return DefaultStateDir
}

// MarkerPath returns the readiness marker path under stateDir.
func MarkerPath(stateDir string) string {
	return filepath.Join(stateDir, ReadyMarker)
}

// WriteReady creates the readiness marker. It is idempotent — re-running
// it on an existing marker is fine.
func WriteReady(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("fmcoord: mkdir %s: %w", stateDir, err)
	}
	path := MarkerPath(stateDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("fmcoord: write marker %s: %w", path, err)
	}
	return f.Close()
}

// RemoveReady removes the readiness marker. A missing marker is a no-op so
// shutdown paths stay tolerant of partially-initialized state.
func RemoveReady(stateDir string) error {
	err := os.Remove(MarkerPath(stateDir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsReady reports whether the readiness marker exists under stateDir.
func IsReady(stateDir string) bool {
	_, err := os.Stat(MarkerPath(stateDir))
	return err == nil
}
