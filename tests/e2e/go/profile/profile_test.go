// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		architecture  string
		reportsTLimit bool
		shutdownC     int
		slowdownC     int
		maxOperatingC int
	}{
		{"a100", "NVIDIA A100-SXM4-40GB", 8, 8, 12, true, false, true, 2, "ampere", false, 92, 87, 83}, // NVSwitch (FabricMgr) but no ComputeDomain fabric block
		{"h100", "NVIDIA H100 80GB HBM3", 8, 8, 18, true, true, true, 2, "hopper", true, 92, 87, 83},
		{"b200", "NVIDIA B200", 8, 8, 0, false, false, true, 2, "blackwell", true, 95, 90, 85}, // NVLink negative control, IB enabled
		{"gb200", "NVIDIA GB200", 8, 8, 18, true, true, true, 4, "blackwell", true, 95, 90, 85},
		{"gb300", "NVIDIA GB300 NVL", 8, 8, 18, true, true, true, 4, "blackwell", true, 95, 90, 85},
		{"l40s", "NVIDIA L40S", 8, 0, 0, false, false, false, 2, "ada_lovelace", true, 96, 93, 89}, // IB + NVLink negative control
		{"t4", "NVIDIA T4", 4, 0, 0, false, false, false, 1, "turing", false, 96, 93, 89},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Load(profilesDir, c.name)
			require.NoError(t, err, "Load(%q)", c.name)
			// One named subtest per derivation so the test output lists every
			// check explicitly (e.g. TestDerivations/a100/ExpectedNV), instead
			// of hiding them inside a single per-profile pass/fail.
			checks := []struct {
				name string
				got  any
				want any
			}{
				{"DisplayName", p.DisplayName, c.displayName},
				{"ExpectedGPUs", p.ExpectedGPUs(), c.gpus},
				{"ExpectedHCAs", p.ExpectedHCAs(), c.hcas},
				{"ExpectedNV", p.ExpectedNV(), c.nv},
				{"FabricMgr", p.FabricMgr(), c.fabricMgr},
				{"HasFabric", p.HasFabric(), c.hasFabric},
				{"IBEnabled", p.IBEnabled(), c.ibEnabled},
				{"ExpectedPCIRoots", p.ExpectedPCIRoots(), c.pciRoots},
				{"Architecture", p.Architecture(), c.architecture},
				{"ReportsTLimitTemp", p.ReportsTLimitTemp(), c.reportsTLimit},
				{"ShutdownThresholdC", p.ShutdownThresholdC(), c.shutdownC},
				{"SlowdownThresholdC", p.SlowdownThresholdC(), c.slowdownC},
				{"MaxOperatingC", p.MaxOperatingC(), c.maxOperatingC},
			}
			for _, ck := range checks {
				t.Run(ck.name, func(t *testing.T) {
					require.Equal(t, ck.want, ck.got, "%s()", ck.name)
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
	require.NoError(t, err, "Load(b200)")
	require.Zero(t, b200.ExpectedNV(), "b200 ExpectedNV() want 0 (standalone, no NVSwitch)")
	require.NotZero(t, b200.ExpectedHCAs(), "b200 ExpectedHCAs() want > 0 (IB is enabled on b200)")

	for _, name := range []string{"l40s", "t4"} {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%s)", name)
		assert.Zero(t, p.ExpectedHCAs(), "%s ExpectedHCAs() want 0 (IB disabled)", name)
		assert.Zero(t, p.ExpectedNV(), "%s ExpectedNV() want 0 (no NVSwitch)", name)
	}
}

// TestUtilizationPercentagesComeFromTheProfile pins which config keys the JPEG
// and OFA accessors read. The shipped profiles all configure 0 % for both, so a
// table over them would agree with an accessor reading the wrong key, or none
// at all — hence a fixture with distinct non-zero values (#637).
func TestUtilizationPercentagesComeFromTheProfile(t *testing.T) {
	dir := t.TempDir()
	yaml := `
device_defaults:
  name: "NVIDIA TEST-GPU"
  utilization:
    gpu: 21
    memory: 22
    jpeg: 35
    ofa: 12
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.yaml"), []byte(yaml), 0o600))

	p, err := Load(dir, "fixture")
	require.NoError(t, err, "Load(fixture)")
	require.Equal(t, 35, p.JPEGUtilizationPct(), "JPEGUtilizationPct() must read utilization.jpeg")
	require.Equal(t, 12, p.OFAUtilizationPct(), "OFAUtilizationPct() must read utilization.ofa")
}

// A profile with no utilization block must report 0 rather than failing to load.
func TestUtilizationPercentagesDefaultToZero(t *testing.T) {
	dir := t.TempDir()
	yaml := `
device_defaults:
  name: "NVIDIA TEST-GPU"
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.yaml"), []byte(yaml), 0o600))

	p, err := Load(dir, "fixture")
	require.NoError(t, err, "Load(fixture)")
	assert.Zero(t, p.JPEGUtilizationPct(), "JPEGUtilizationPct()")
	assert.Zero(t, p.OFAUtilizationPct(), "OFAUtilizationPct()")
}

// TestAll ensures every shipped profile loads cleanly.
func TestAll(t *testing.T) {
	ps, err := All(profilesDir)
	require.NoError(t, err, "All()")
	require.Len(t, ps, len(KnownProfiles), "All() returned wrong count")
}
