// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/internal/gpuarch"
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
		reportsTLimit bool
		c2c           bool
		shutdownC     int
		slowdownC     int
		maxOperatingC int
		maxLinkGen    int
		// graphicsMaxMHz is clocks.graphics_max, which the mock also reports as
		// the OEM ceiling in Max Customer Boost Clocks (#712). Every value here
		// is the max_clocks/graphics_clock its board reports in
		// tests/e2e/go/assertions/nvidiasmi/testdata/hardware, so a profile
		// edited away from its capture fails here.
		graphicsMaxMHz int
	}{
		{"a100", "NVIDIA A100-SXM4-40GB", 8, 8, 12, true, false, true, 2, false, false, 92, 87, 83, 4, 1410}, // NVSwitch (FabricMgr) but no ComputeDomain fabric block
		{"h100", "NVIDIA H100 80GB HBM3", 8, 8, 18, true, true, true, 2, true, false, 92, 87, 83, 5, 1980},
		{"b200", "NVIDIA B200", 8, 8, 0, false, false, true, 2, true, false, 95, 90, 85, 6, 1965}, // NVLink negative control, IB enabled
		{"gb200", "NVIDIA GB200", 4, 4, 18, true, true, true, 2, true, true, 95, 90, 85, 6, 2062}, // one NVL72 compute tray: 2 superchips, 4 GPUs
		{"gb300", "NVIDIA GB300 NVL", 4, 4, 18, true, true, true, 2, true, true, 95, 90, 85, 6, 2070},
		{"l40s", "NVIDIA L40S", 8, 0, 0, false, false, false, 2, true, false, 96, 93, 89, 4, 2520}, // IB + NVLink negative control
		{"t4", "NVIDIA T4", 4, 0, 0, false, false, false, 1, false, false, 96, 93, 89, 3, 1590},
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
				{"ReportsTLimitTemp", p.ReportsTLimitTemp(), c.reportsTLimit},
				{"C2CEnabled", p.C2CEnabled(), c.c2c},
				{"ShutdownThresholdC", p.ShutdownThresholdC(), c.shutdownC},
				{"SlowdownThresholdC", p.SlowdownThresholdC(), c.slowdownC},
				{"MaxOperatingC", p.MaxOperatingC(), c.maxOperatingC},
				{"MaxPCIeLinkGen", p.MaxPCIeLinkGen(), c.maxLinkGen},
				{"GraphicsMaxClockMHz", p.GraphicsMaxClockMHz(), c.graphicsMaxMHz},
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
	// Load requires device_defaults.architecture, so every fixture in this
	// file declares one. Where the test is architecture-agnostic, as here, the
	// value is incidental and carries no meaning for what is being checked.
	yaml := `
device_defaults:
  name: "NVIDIA TEST-GPU"
  architecture: "hopper"
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
  architecture: "hopper"
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

// TestWorkloadPowerProfilesAreBlackwellOnDriver570 pins both axes the e2e
// expectation is derived from, so "lists its profiles" cannot quietly become
// "always lists them". b200 is the interesting case: Blackwell silicon, but the
// profile pins driver 560, which predates the API — so it must be declined even
// though the architecture supports the feature.
func TestWorkloadPowerProfilesAreBlackwellOnDriver570(t *testing.T) {
	wantSupported := map[string]bool{"gb200": true, "gb300": true}
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%q)", name)

		require.Equal(t, wantSupported[name], p.SupportsWorkloadPowerProfiles(),
			"%s: power-profiles support should be %v (driver %d.x)",
			name, wantSupported[name], p.DriverMajor())

		profiles, declared := p.WorkloadPowerProfiles()
		if !wantSupported[name] {
			require.False(t, declared,
				"%s: declares workload profiles its driver cannot expose", name)
			continue
		}
		require.NotEmpty(t, profiles, "%s: declared the feature but lists no profile", name)
		require.Empty(t, p.RequestedWorkloadPowerProfiles(),
			"%s: every hardware capture reports no requested profile", name)

		// Ascending and unique: the ids double as bit positions in NVML's
		// 255-bit mask, so a duplicate would silently collapse.
		seen := map[int]bool{}
		for i, wp := range profiles {
			require.False(t, seen[wp.ID], "%s: duplicate profile id %d", name, wp.ID)
			seen[wp.ID] = true
			require.Less(t, wp.ID, 255, "%s: profile id %d has no bit in a 255-bit mask", name, wp.ID)
			if i > 0 {
				require.Greater(t, wp.ID, profiles[i-1].ID, "%s: profiles should ascend by id", name)
			}
		}
	}
}

// TestWorkloadProfilePairsBackTheSetterAssertions checks every profile that
// supports the feature offers both pairs the `-sr` / `-cr` e2e needs. Without
// them those assertions skip silently, so a profile that lost its conflicts
// would quietly stop exercising arbitration.
func TestWorkloadProfilePairsBackTheSetterAssertions(t *testing.T) {
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%q)", name)
		if !p.SupportsWorkloadPowerProfiles() {
			continue
		}

		first, second, ok := p.IndependentWorkloadProfilePair()
		require.True(t, ok, "%s: no two profiles that can be enforced together", name)
		require.NotEqual(t, first, second, "%s: independent pair is one profile twice", name)

		winner, loser, ok := p.ConflictingWorkloadProfilePair()
		require.True(t, ok, "%s: no two conflicting profiles with distinct priorities", name)

		profiles, _ := p.WorkloadPowerProfiles()
		byID := make(map[int]WorkloadPowerProfile, len(profiles))
		for _, wp := range profiles {
			byID[wp.ID] = wp
		}

		// Lower priority value wins, matching NVML's arbitration.
		require.Less(t, byID[winner].Priority, byID[loser].Priority,
			"%s: %d should outrank %d", name, winner, loser)
		require.True(t,
			slices.Contains(byID[winner].Conflicts, loser) ||
				slices.Contains(byID[loser].Conflicts, winner),
			"%s: %d and %d are not declared as conflicting", name, winner, loser)
		require.False(t,
			slices.Contains(byID[first].Conflicts, second) ||
				slices.Contains(byID[second].Conflicts, first),
			"%s: %d and %d conflict, so both cannot be enforced", name, first, second)
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
  architecture: "hopper"
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.yaml"), []byte(yaml), 0o600))

	p, err := Load(dir, "fixture")
	require.NoError(t, err, "Load(fixture)")
	assert.False(t, p.ReportsRowRemapHistogram(), "ReportsRowRemapHistogram()")
}

// TestExpectedPCIRootsFallsBackToOneRoot pins the assumption Load makes for a
// profile with no pcie_topology block: the pcibus simulator synthesizes one flat
// root complex (internal/agent/pcibus flatTopology), so PCISysfs must expect
// exactly one. No shipped profile omits the block, so nothing else covers this.
func TestExpectedPCIRootsFallsBackToOneRoot(t *testing.T) {
	dir := t.TempDir()
	const raw = `
device_defaults:
  name: "NVIDIA Mock GPU"
  architecture: "hopper"
devices:
  - index: 0
    pci:
      bus_id: "0000:1A:00.0"
  - index: 1
    pci:
      bus_id: "0000:1B:00.0"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flat.yaml"), []byte(raw), 0o600))

	p, err := Load(dir, "flat")
	require.NoError(t, err)
	require.Equal(t, 1, p.ExpectedPCIRoots(), "topology-less profile spans one synthesized root")
	require.Equal(t, 2, p.ExpectedGPUs())
}

// TestProfileArchitectures pins each shipped profile to its generation. Every
// "this generation and newer" expectation in the harness keys off
// Architecture(), so a profile silently retagged would otherwise flip those
// assertions to their negative branch and still pass.
func TestProfileArchitectures(t *testing.T) {
	t.Parallel()
	want := map[string]gpuarch.Arch{
		"a100":  gpuarch.Ampere,
		"h100":  gpuarch.Hopper,
		"b200":  gpuarch.Blackwell,
		"gb200": gpuarch.Blackwell,
		"gb300": gpuarch.Blackwell,
		"l40s":  gpuarch.Ada,
		"t4":    gpuarch.Turing,
	}
	for _, name := range KnownProfiles {
		p, err := Load(profilesDir, name)
		require.NoError(t, err, "Load(%q)", name)
		require.Equal(t, want[name], p.Architecture(), "%s architecture", name)
	}
}

// TestLoadRejectsUnknownArchitecture covers the strictness the harness needs
// and the engine does not: these are our own chart profiles, so an
// unrecognized spelling is a typo in the repository. Accepting it would leave
// every architecture gate answering its negative branch with the suite green.
func TestLoadRejectsUnknownArchitecture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// The positive control shares everything with the negative one but the
	// architecture, so a failure cannot be blamed on the stub profile.
	const template = `device_defaults:
  name: "NVIDIA TEST"
  architecture: %q
devices:
  - index: 0
`
	write := func(t *testing.T, name, architecture string) {
		t.Helper()
		body := fmt.Sprintf(template, architecture)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600))
	}

	write(t, "good", "hopper")
	p, err := Load(dir, "good")
	require.NoError(t, err)
	require.Equal(t, gpuarch.Hopper, p.Architecture())

	write(t, "typo", "hopperr")
	_, err = Load(dir, "typo")
	require.ErrorContains(t, err, "hopperr")

	// An omitted key reports as an omission, not as a misspelling of "".
	write(t, "absent", "")
	_, err = Load(dir, "absent")
	require.ErrorContains(t, err, "device_defaults.architecture is empty")
}

// The MIG-capable boards declare a uniform partitioning, which is what makes
// them usable with the device plugin's migStrategy=single — the strategy
// refuses a node whose MIG devices are not all the same profile.
func TestMIGPartitionsComeFromTheProfile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		partitions    int
		uniformDevice string
		capable       bool
	}{
		{"a100", 7, "1g.5gb", true},
		{"h100", 7, "1g.10gb", true},
		// Capable boards that ship no default layout. They are why capability
		// cannot be read off the layout: reporting these as non-MIG hardware
		// is what silently excused them from the MIG suite.
		{"b200", 0, "", true},
		{"gb200", 0, "", true},
		{"gb300", 0, "", true},
		// Not MIG-capable boards, and the negative control for the accessors:
		// a profile with no mig block must report no partitions rather than a
		// zero-valued one that reads as "declared but empty".
		{"l40s", 0, "", false},
		{"t4", 0, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := Load(profilesDir, tc.name)
			require.NoError(t, err)
			require.Equal(t, tc.partitions, p.MIGPartitionsPerGPU())
			require.Equal(t, tc.uniformDevice, p.MIGDeviceProfile())
			require.Equal(t, tc.capable, p.MIGCapable())
			require.Equal(t, tc.partitions > 0, p.MIGDeclaresLayout())
		})
	}
}

// MIGDeviceProfile is what the migStrategy=single assertion keys on, so a
// non-uniform layout has to report empty rather than silently picking one of
// the profiles and asserting against a resource name the plugin never
// publishes.
func TestMIGDeviceProfileIsEmptyForMixedLayouts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const raw = `
device_defaults:
  name: "NVIDIA Mock GPU"
  mig:
    mode_current: "enabled"
    max_gpu_instances: 7
    gpu_instances:
      - profile: "1g.5gb"
        count: 2
      - profile: "3g.20gb"
        count: 1
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mixed.yaml"), []byte(raw), 0o600))

	p, err := Load(dir, "mixed")
	require.NoError(t, err)
	require.Equal(t, 3, p.MIGPartitionsPerGPU(), "a mixed layout still declares three partitions")
	require.Empty(t, p.MIGDeviceProfile(), "a mixed layout has no single device profile")
	require.True(t, p.MIGCapable())
	require.True(t, p.MIGDeclaresLayout())
}

// A count left unset means one instance, matching how the engine reads the
// same field; a profile that omits it must not contribute zero partitions.
func TestMIGPartitionCountDefaultsToOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const raw = `
device_defaults:
  name: "NVIDIA Mock GPU"
  mig:
    gpu_instances:
      - profile: "7g.40gb"
devices:
  - index: 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "single.yaml"), []byte(raw), 0o600))

	p, err := Load(dir, "single")
	require.NoError(t, err)
	require.Equal(t, 1, p.MIGPartitionsPerGPU())
	require.Equal(t, "7g.40gb", p.MIGDeviceProfile())
}
