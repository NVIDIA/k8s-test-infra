// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package gid provides InfiniBand GID / port-GUID string helpers shared by
// sysfs scanning and the mock-ib daemon.
package gid

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

// Normalize lowercases and trims a GID string (colon-separated or compact hex).
func Normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Format renders a 16-byte GID in canonical sysfs form (8 colon-separated pairs).
func Format(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x:%02x%02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7],
		b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}

// PortGUIDFromBytes extracts the port GUID from an IB GID (lower 64 bits).
func PortGUIDFromBytes(gid []byte) string {
	if len(gid) != 16 {
		return ""
	}
	b := gid[8:16]
	return registry.NormalizePortGUID(fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7]))
}

// ParseInto decodes a GID string into dst (16 bytes). Invalid input is ignored.
func ParseInto(dst []byte, s string) {
	if len(dst) != 16 {
		return
	}
	h := strings.NewReplacer(":", "").Replace(s)
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 16 {
		return
	}
	copy(dst, b)
}
