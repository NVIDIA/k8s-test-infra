// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"strings"
)

// Env* are the environment variable names the mock-ib daemon reads at startup.
const (
	envMockIBPeers           = "MOCK_IB_PEERS"
	envMockIBPingServiceHost = "MOCK_IB_PING_SERVICE_HOST"
)

// envOr returns getenv(key) or def when unset.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parsePeerList splits a comma-separated peer IP list.
func parsePeerList(s string) []string {
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
