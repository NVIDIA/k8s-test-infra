// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package ibutil holds the pure LID/GUID normalizers used by the ibping
// assertion. They are pure functions (no k8s/exec imports, no build tag) so
// their table-driven unit tests run in the normal `go test ./...` fast CI.
//
// Ported from tests/e2e/validate-ibping.sh:
//   - normalize_lid_for_ibping:  decimal stays decimal; 0x<hex> -> decimal.
//   - normalize_guid_for_ibping: strip ':' and 0x, re-prefix "0x".
package ibutil

import (
	"errors"
	"strconv"
	"strings"
)

func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// NormalizeLID converts a sysfs LID (decimal or 0x-hex) to the decimal form
// `ibping` accepts. ibping also takes hex, but the script normalizes to decimal
// for portability, so we match that.
func NormalizeLID(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty LID")
	}
	if isDecimal(s) {
		return s, nil
	}
	hex := strings.TrimPrefix(s, "0x")
	hex = strings.TrimPrefix(hex, "0X")
	v, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(v, 10), nil
}

// NormalizeGUID converts a colon-separated sysfs port GUID into the
// `ibping -G` form: a single 0x-prefixed hex string with no colons.
func NormalizeGUID(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, ":", "")
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return "", errors.New("empty GUID")
	}
	return "0x" + strings.ToLower(s), nil
}
