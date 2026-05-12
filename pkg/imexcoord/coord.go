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

// Package imexcoord implements the shared-volume coordination protocol
// used by the fake IMEX binaries (cmd/fake-imex/{daemon,ctl}) to
// simulate ComputeDomain peer discovery on KIND clusters without real
// GB200 hardware. See NVIDIA/k8s-test-infra#304 for the full design.
//
// Protocol (single-clique view; the real compute-domain-daemon writes a
// per-clique nodes.cfg so the fake never has to know cliques itself):
//
//  1. The fake nvidia-imex daemon writes an empty marker file at
//     <stateDir>/<pod-ip> on startup and removes it on SIGTERM/SIGINT.
//  2. The fake nvidia-imex-ctl readiness probe reads the same
//     nodes.cfg the real daemon writes, looks up each peer IP under
//     <stateDir>, and reports READY iff every peer's marker exists.
//
// Pod IPs are globally unique in Kubernetes, so no clique
// subdirectories are required — the daemon's own filtering already
// scopes the nodes.cfg to the correct clique.
//
// # Known limitations
//
// This is a deliberately thin file-based simulation of nvidia-imex; it
// does not attempt to mirror every property of the real network-based
// protocol. Three behaviours are worth knowing about:
//
//  1. No liveness signal. Markers are presence-only, so SIGKILL,
//     OOM-kill, kubelet eviction, or a node crash all leave stale
//     markers behind. nvidia-imex-ctl will report READY for a fault
//     window equal to whatever cleans the shared hostPath — until then
//     the upstream ComputeDomain controller is responsible for
//     tolerating a stale READY across that window.
//  2. Pod IP recycling is undefined. Markers are keyed by POD_IP, and
//     Kubernetes may eventually reissue an IP after a long enough
//     delete-recreate cycle. If a new pod inherits an IP that has a
//     leftover marker from a prior incarnation, ctl will treat the new
//     pod as immediately ready. KIND tears the shared hostPath down at
//     cluster destroy, so this is bounded in practice; long-lived
//     shared-mount simulations would need to clear markers on pod
//     deletion.
//  3. The daemon's 2s tick is observability-only — it re-reads
//     nodes.cfg for logging and re-asserts its marker, but readiness
//     itself is driven by ctl invocations from the kubelet probe. The
//     worst-case latency between a peer dying and ctl observing it is
//     the readiness probe period, not the daemon's tick.
package imexcoord

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultStateDir is the canonical hostPath shared between KIND
// workers. KIND mounts /tmp/nvml-mock-imex-state on the host to this
// path inside each node container via extraMounts.
const DefaultStateDir = "/var/lib/nvml-mock/imex-state"

// DefaultNodesConfig matches the path the upstream compute-domain-daemon
// uses when it writes its peer list (see writeDaemonsConfig() in
// kubernetes-sigs/dra-driver-nvidia-gpu).
const DefaultNodesConfig = "/imexd/nodes.cfg"

// EnvStateDir and EnvNodesConfig override DefaultStateDir and
// DefaultNodesConfig respectively. Both fakes honour the same env vars
// so tests can run them against a temp directory without root.
const (
	EnvStateDir    = "IMEX_STATE_DIR"
	EnvNodesConfig = "IMEX_NODES_CONFIG"
	EnvPodIP       = "POD_IP"
)

// StateDir returns the effective state directory, falling back to
// DefaultStateDir if EnvStateDir is empty.
func StateDir() string {
	if v := os.Getenv(EnvStateDir); v != "" {
		return v
	}
	return DefaultStateDir
}

// NodesConfigPath returns the effective nodes.cfg path.
func NodesConfigPath() string {
	if v := os.Getenv(EnvNodesConfig); v != "" {
		return v
	}
	return DefaultNodesConfig
}

// MarkerPath returns the marker filename for the given peer IP.
func MarkerPath(stateDir, ip string) string {
	return filepath.Join(stateDir, ip)
}

// WriteMarker creates an empty marker file at <stateDir>/<ip>. It is
// idempotent — re-running it on an existing marker is fine.
func WriteMarker(stateDir, ip string) error {
	if ip == "" {
		return fmt.Errorf("imexcoord: empty pod IP, cannot write marker")
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("imexcoord: mkdir %s: %w", stateDir, err)
	}
	path := MarkerPath(stateDir, ip)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("imexcoord: write marker %s: %w", path, err)
	}
	return f.Close()
}

// RemoveMarker removes a marker file. Missing files are not an error so
// shutdown paths can call this unconditionally.
func RemoveMarker(stateDir, ip string) error {
	if ip == "" {
		return nil
	}
	err := os.Remove(MarkerPath(stateDir, ip))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReadPeers parses nodes.cfg and returns the list of peer IPs in the
// order they appear. Blank lines and lines starting with '#' are
// ignored, matching the upstream daemon's writer.
func ReadPeers(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var peers []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// nodes.cfg may contain `ip` or `ip port`; keep only the IP.
		if idx := strings.IndexAny(line, " \t"); idx >= 0 {
			line = line[:idx]
		}
		peers = append(peers, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return peers, nil
}

// AllPeersReady returns true iff every peer in `peers` has a marker
// file under stateDir. An empty peer list is treated as "ready"
// (single-node clique — there is nothing to wait for).
func AllPeersReady(stateDir string, peers []string) (bool, string) {
	for _, p := range peers {
		if _, err := os.Stat(MarkerPath(stateDir, p)); err != nil {
			return false, p
		}
	}
	return true, ""
}
