// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package gid provides InfiniBand GID / port-GUID string helpers shared by
// sysfs scanning and the mock-ib daemon.
package gid

import (
	"encoding/hex"
	"fmt"
	"strings"
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
	return NormalizePortGUID(fmt.Sprintf("%02x%02x:%02x%02x:%02x%02x:%02x%02x",
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

// NormalizePortGUID lowercases a port GUID and formats it with colon separators
// (a088:c203:00ab:0001). Non-hex characters are stripped; short inputs are
// left-padded to 16 hex digits.
func NormalizePortGUID(s string) string {
	var b strings.Builder

	for _, c := range strings.ToLower(s) {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			b.WriteByte(byte(c))
		}
	}

	hex := b.String()

	if len(hex) < 16 {
		hex = strings.Repeat("0", 16-len(hex)) + hex
	} else if len(hex) > 16 {
		hex = hex[len(hex)-16:]
	}

	return hex[0:4] + ":" + hex[4:8] + ":" + hex[8:12] + ":" + hex[12:16]
}
