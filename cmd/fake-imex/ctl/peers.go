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

package main

// ExpectedPeers returns the IP set the readiness check must observe
// markers for. It is the union of the nodes.cfg peer list (written by
// the upstream compute-domain-daemon) and, when set, the local pod's
// own IP from POD_IP.
//
// Why include POD_IP: when nodes.cfg happens to be silent about the
// local pod (daemon bug, torn write, or clique-split scenario),
// AllPeersReady would only verify peer markers and could report READY
// while the local daemon is dead. Including ownIP in the check turns
// that case into a not-ready exit, which is what the kubelet readiness
// probe is supposed to drive.
//
// Idempotent: if ownIP is empty or already present in peers, the
// input slice is returned unchanged.
func ExpectedPeers(peers []string, ownIP string) []string {
	if ownIP == "" {
		return peers
	}
	for _, p := range peers {
		if p == ownIP {
			return peers
		}
	}
	return append(append([]string{}, peers...), ownIP)
}
