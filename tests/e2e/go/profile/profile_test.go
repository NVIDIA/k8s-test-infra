// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

import "testing"

// profilesDir is the chart profiles directory relative to this test package
// (tests/e2e/go/profile -> repo root -> deployments/...). `go test` runs with the
// package directory as the working directory.
const profilesDir = "../../../../deployments/nvml-mock/helm/nvml-mock/profiles"

// TestDerivations cross-checks the values derived from the real chart profile
// YAML against an authoritative table. The NV# column matches the engine
// oracle constants in
// pkg/gpu/mocknvml/engine/topology_test.go:TestNodeFabric_BuiltinProfiles
// (a100 NV12; h100/gb200/gb300 NV18; b200 NV0). Keeping this table in lockstep
// with that oracle is the guard that stops the chart profiles/ and engine
// configs/ copies from drifting in a way the e2e would not catch.
func TestDerivations(t *testing.T) {
	cases := []struct {
		name          string
		displayName   string
		gpus          int
		hcas          int
		nv            int
		fabricMgr     bool
		hasFabric     bool
		ibEnabled     bool
		pciRoots      int
		driverVersion string
	}{
		{"a100", "NVIDIA A100-SXM4-40GB", 8, 8, 12, true, false, true, 2, "550.163.01"}, // NVSwitch (FabricMgr) but no ComputeDomain fabric block
		{"h100", "NVIDIA H100 80GB HBM3", 8, 8, 18, true, true, true, 2, "550.163.01"},
		{"b200", "NVIDIA B200", 8, 8, 0, false, false, true, 2, "560.35.03"}, // NVLink negative control, IB enabled
		{"gb200", "NVIDIA GB200", 8, 8, 18, true, true, true, 4, "580.65.06"},
		{"gb300", "NVIDIA GB300 NVL", 8, 8, 18, true, true, true, 4, "570.124.06"},
		{"l40s", "NVIDIA L40S", 8, 0, 0, false, false, false, 2, "550.163.01"}, // IB + NVLink negative control
		{"t4", "NVIDIA T4", 4, 0, 0, false, false, false, 1, "550.163.01"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Load(profilesDir, c.name)
			if err != nil {
				t.Fatalf("Load(%q): %v", c.name, err)
			}
			// One named subtest per derivation so the test output lists every
			// check explicitly (e.g. TestDerivations/a100/ExpectedNV), instead
			// of hiding them inside a single per-profile pass/fail.
			checks := []struct {
				name string
				got  any
				want any
			}{
				{"DisplayName", p.DisplayName, c.displayName},
				{"DriverVersion", p.DriverVersion, c.driverVersion},
				{"ExpectedGPUs", p.ExpectedGPUs(), c.gpus},
				{"ExpectedHCAs", p.ExpectedHCAs(), c.hcas},
				{"ExpectedNV", p.ExpectedNV(), c.nv},
				{"FabricMgr", p.FabricMgr(), c.fabricMgr},
				{"HasFabric", p.HasFabric(), c.hasFabric},
				{"IBEnabled", p.IBEnabled(), c.ibEnabled},
				{"ExpectedPCIRoots", p.ExpectedPCIRoots(), c.pciRoots},
			}
			for _, ck := range checks {
				t.Run(ck.name, func(t *testing.T) {
					if ck.got != ck.want {
						t.Errorf("%s() = %v, want %v", ck.name, ck.got, ck.want)
					}
				})
			}
		})
	}
}

// TestNegativeControlsAreIndependent pins the binding invariant that IB-disabled
// and NVLink-disabled are independent axes: b200 must report HCAs (IB enabled)
// while asserting NV0; l40s/t4 must report 0 HCAs AND NV0.
func TestNegativeControlsAreIndependent(t *testing.T) {
	b200, err := Load(profilesDir, "b200")
	if err != nil {
		t.Fatalf("Load(b200): %v", err)
	}
	if b200.ExpectedNV() != 0 {
		t.Errorf("b200 ExpectedNV() = %d, want 0 (standalone, no NVSwitch)", b200.ExpectedNV())
	}
	if b200.ExpectedHCAs() == 0 {
		t.Errorf("b200 ExpectedHCAs() = 0, want > 0 (IB is enabled on b200)")
	}

	for _, name := range []string{"l40s", "t4"} {
		p, err := Load(profilesDir, name)
		if err != nil {
			t.Fatalf("Load(%s): %v", name, err)
		}
		if p.ExpectedHCAs() != 0 {
			t.Errorf("%s ExpectedHCAs() = %d, want 0 (IB disabled)", name, p.ExpectedHCAs())
		}
		if p.ExpectedNV() != 0 {
			t.Errorf("%s ExpectedNV() = %d, want 0 (no NVSwitch)", name, p.ExpectedNV())
		}
	}
}

// TestAll ensures every shipped profile loads cleanly.
func TestAll(t *testing.T) {
	ps, err := All(profilesDir)
	if err != nil {
		t.Fatalf("All(): %v", err)
	}
	if len(ps) != len(KnownProfiles) {
		t.Fatalf("All() returned %d profiles, want %d", len(ps), len(KnownProfiles))
	}
}
