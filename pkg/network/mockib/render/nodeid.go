// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import "hash/fnv"

// nodeID returns a stable 16-bit id from a Kubernetes node name.
func nodeID(nodeName string) uint16 {
	if nodeName == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(nodeName))
	return uint16(h.Sum32())
}
