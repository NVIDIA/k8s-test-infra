// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import "testing"

func TestParseLID(t *testing.T) {
	tests := []struct {
		in      string
		want    uint16
		wantErr bool
	}{
		{"768", 768, false},
		{"0x0300", 0x0300, false},
		{"", 0, true},
		{"not-a-lid", 0, true},
	}
	for _, tc := range tests {
		got, err := parseLID(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseLID(%q): want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLID(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseLID(%q) = 0x%04x want 0x%04x", tc.in, got, tc.want)
		}
	}
}
