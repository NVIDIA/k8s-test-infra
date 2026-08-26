// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatPortState(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"DOWN", "1: DOWN\n"},
		{"INIT", "2: INIT\n"},
		{"ARMED", "3: ARMED\n"},
		{"ACTIVE", "4: ACTIVE\n"},
		{"", "4: ACTIVE\n"},
		{"ACTIVE_DEFER", "5: ACTIVE_DEFER\n"},
		{"unknown", "4: ACTIVE\n"},
	}
	for _, tc := range tests {
		got := formatPortState(tc.in)
		require.Equal(t, tc.want, got, "formatPortState(%q)", tc.in)
	}
}

func TestFormatPhysState(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"DISABLED", "3: Disabled\n"},
		{"POLLING", "2: Polling\n"},
		{"TRAINING", "4: Training\n"},
		{"LINKUP", "5: LinkUp\n"},
		{"", "5: LinkUp\n"},
		{"LINKERRORRECOVERY", "6: LinkErrorRecovery\n"},
		{"PHYTEST", "7: Phy Test\n"},
		{"other", "5: LinkUp\n"},
	}
	for _, tc := range tests {
		got := formatPhysState(tc.in)
		require.Equal(t, tc.want, got, "formatPhysState(%q)", tc.in)
	}
}
