// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nodeid derives a stable 16-bit identifier from a Kubernetes node
// name. Used by render and counters to seed per-node LIDs, GUIDs, and
// counter values deterministically.
package nodeid

import "hash/fnv"

// NodeID returns a stable 16-bit id from a Kubernetes node name. The empty
// string is mapped to 0 (matches the legacy behavior in render).
func NodeID(nodeName string) uint16 {
	if nodeName == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(nodeName))
	return uint16(h.Sum32())
}
