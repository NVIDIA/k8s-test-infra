// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"strconv"
	"strings"
)

const (
	EnvMockIBConfig     = "MOCK_IB_CONFIG"
	EnvGPUCount         = "GPU_COUNT"
	EnvNodeName         = "NODE_NAME"
	EnvMockIBRoot       = "MOCK_IB_ROOT"
	EnvMockIBPingSocket = "MOCK_IB_PING_SOCKET"
	EnvMockIBPingPort   = "MOCK_IB_PING_PORT"
	EnvMockIBPingFabric = "MOCK_IB_PING_FABRIC"
	EnvMockIBPeers           = "MOCK_IB_PEERS"
	EnvMockIBPingServiceHost = "MOCK_IB_PING_SERVICE_HOST"
)

// EnvOr returns getenv(key) or def when unset.
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvIntOr parses getenv(key) as int or returns def when unset/invalid.
func EnvIntOr(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// EnvBoolOr returns true when getenv(key) is 1/true (case-insensitive).
func EnvBoolOr(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v == "1" || strings.EqualFold(v, "true")
}

// ParsePeerList splits a comma-separated peer IP list.
func ParsePeerList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
