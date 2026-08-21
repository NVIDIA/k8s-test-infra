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
		c2c           bool
		shutdownC     int
		slowdownC     int
		maxOperatingC int
		maxLinkGen    int
	}{
		{"a100", "NVIDIA A100-SXM4-40GB", 8, 8, 12, true, false, true, 2, "ampere", false, false, 92, 87, 83, 4}, // NVSwitch (FabricMgr) but no ComputeDomain fabric block
		{"h100", "NVIDIA H100 80GB HBM3", 8, 8, 18, true, true, true, 2, "hopper", true, false, 92, 87, 83, 5},
		{"b200", "NVIDIA B200", 8, 8, 0, false, false, true, 2, "blackwell", true, false, 95, 90, 85, 6}, // NVLink negative control, IB enabled
		{"gb200", "NVIDIA GB200", 8, 8, 18, true, true, true, 4, "blackwell", true, true, 95, 90, 85, 6},
		{"gb300", "NVIDIA GB300 NVL", 8, 8, 18, true, true, true, 4, "blackwell", true, true, 95, 90, 85, 6},
		{"l40s", "NVIDIA L40S", 8, 0, 0, false, false, false, 2, "ada_lovelace", true, false, 96, 93, 89, 4}, // IB + NVLink negative control
		{"t4", "NVIDIA T4", 4, 0, 0, false, false, false, 1, "turing", false, false, 96, 93, 89, 3},
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
				{"C2CEnabled", p.C2CEnabled(), c.c2c},
				{"ShutdownThresholdC", p.ShutdownThresholdC(), c.shutdownC},
				{"SlowdownThresholdC", p.SlowdownThresholdC(), c.slowdownC},
				{"MaxOperatingC", p.MaxOperatingC(), c.maxOperatingC},
				{"MaxPCIeLinkGen", p.MaxPCIeLinkGen(), c.maxLinkGen},
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

// TestC2CIsGraceOnly pins C2C as a Grace-only axis. gb200/gb300 declare the
// link; every other shipped profile must report false, including b200, which is
// Blackwell but has no Grace CPU. Without this, a profile-derived e2e
// expectation could quietly become "always Enabled". See issue #639.
//
// Driven from KnownProfiles so a newly added profile has to declare which side
// it belongs on rather than defaulting into the untested one.
func TestC2CIsGraceOnly(t *testing.T) {
	graceProfiles := map[string]bool{"gb200": true, "gb300": true}
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%q)", name)
		require.Equal(t, graceProfiles[name], p.C2CEnabled(),
			"%s: nvlink.c2c_enabled should be %v", name, graceProfiles[name])
	}
}

// TestPlatformIdentityIsRackScaleOnly pins platform identity as a rack-scale
// axis, for the same reason as the C2C one: an e2e expectation derived from the
// profiles must keep a negative control, or "reports a location" could quietly
// become "always reports one". b200 is the interesting case — Blackwell, but a
// board in no rack. See issue #642.
func TestPlatformIdentityIsRackScaleOnly(t *testing.T) {
	rackProfiles := map[string]bool{"gb200": true, "gb300": true}
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%q)", name)
		identity, declared := p.PlatformIdentity()
		require.Equal(t, rackProfiles[name], declared,
			"%s: device_defaults.platform should be declared=%v", name, rackProfiles[name])
		if !declared {
			require.Empty(t, identity.ModuleIDs, "%s: module ids without a platform block", name)
			continue
		}
		require.Len(t, identity.ModuleIDs, p.ExpectedGPUs(), "%s: a module id per GPU", name)
		require.NotEmpty(t, identity.ChassisSerialNumber, "%s: chassis_serial_number", name)
	}
}

// The per-device module ids must survive the profile decode distinctly: they are
// the only field that tells one of a node's GPUs from another, and a decode that
// read them from device_defaults alone would hand every GPU the same one.
func TestPlatformIdentityModuleIDsAreDistinct(t *testing.T) {
	for _, name := range []string{"gb200", "gb300"} {
		t.Run(name, func(t *testing.T) {
			p, err := Load(profilesDir, name)
			require.NoError(t, err, "Load(%q)", name)
			identity, declared := p.PlatformIdentity()
			require.True(t, declared, "declares a platform block")

			seen := map[int]int{}
			for i, id := range identity.ModuleIDs {
				require.NotZero(t, id, "device %d module id", i)
				prev, dup := seen[id]
				require.False(t, dup, "device %d shares module id %d with device %d", i, id, prev)
				seen[id] = i
			}
		})
	}
}

// TestRowRemapHistogramIsAmpereAndLater pins the histogram to the same
// architecture axis nvidia-smi uses for the SRAM layout: row remapping arrived
// with Ampere, so t4 must leave remapped_rows.availability_histogram unset and
// report unsupported, while every later profile configures it. Requiring the two
// accessors to agree is what stops a profile from configuring capacity for a
// generation whose driver output has no place to report it. Driven from
// KnownProfiles so a newly added profile has to declare which side it belongs on
// (#641).
func TestRowRemapHistogramIsAmpereAndLater(t *testing.T) {
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%q)", name)
		want := p.ReportsDetailedSramECC()
		require.Equal(t, want, p.ReportsRowRemapHistogram(),
			"%s (%s): remapped_rows.availability_histogram configured should be %v",
			name, p.Architecture(), want)
		if want {
			require.Positive(t, p.RowRemapHistogramBanks(),
				"%s: availability_histogram.max must be a real bank count", name)
		}
	}
}

// The SRAM layout is keyed on the architecture nvidia-smi reads, so t4 is the
// only shipped profile on the combined side. Pinning it by name as well as by
// architecture catches a profile that changes its architecture without the
// expectation following.
func TestDetailedSramECCIsAmpereAndLater(t *testing.T) {
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%q)", name)
		require.Equal(t, name != "t4", p.ReportsDetailedSramECC(),
			"%s (%s): detailed SRAM ECC rendering", name, p.Architecture())
	}
}

// A profile with no remapped_rows block must load and report the histogram
// unsupported rather than failing.
func TestRowRemapHistogramDefaultsToUnsupported(t *testing.T) {
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
	assert.False(t, p.ReportsRowRemapHistogram(), "ReportsRowRemapHistogram()")
}
