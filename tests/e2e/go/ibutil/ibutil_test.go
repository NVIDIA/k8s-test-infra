// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ibutil

import "testing"

func TestNormalizeLID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"16", "16", false},
		{"0x0001", "1", false},
		{"0x000a", "10", false},
		{"0xFFFF", "65535", false},
		{"  0x10  ", "16", false},
		{"", "", true},
		{"0xZZ", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeLID(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeLID(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("NormalizeLID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeGUID(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"a288:c203:00ab:1234", "0xa288c20300ab1234", false},
		{"0xa288c20300ab1234", "0xa288c20300ab1234", false},
		{"A288:C203:00AB:1234", "0xa288c20300ab1234", false},
		{"  a288c2:0300:ab00  ", "0xa288c20300ab00", false},
		{":::", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeGUID(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeGUID(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("NormalizeGUID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
