// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import "hash/fnv"

// nodeID returns a stable 32-bit id from a Kubernetes node name.
func nodeID(nodeName string) uint32 {
	if nodeName == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(nodeName))
	return h.Sum32()
}
