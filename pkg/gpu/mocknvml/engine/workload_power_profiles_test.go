// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// blackwellProfiles is a deliberately non-contiguous set: it is what tells a
// perfProfile array indexed by profile id apart from a densely packed one.
// nvidia-smi reads perfProfile[id], so packing renders the wrong priority for
// every profile past the first gap.
func blackwellProfiles() *WorkloadPowerProfilesConfig {
	return &WorkloadPowerProfilesConfig{
		Supported: []WorkloadPowerProfileConfig{
			{ID: 0, Priority: 10, Conflicts: []uint32{1, 5}}, // max_p
			{ID: 1, Priority: 20, Conflicts: []uint32{0, 5}}, // max_q
			{ID: 5, Priority: 30, Conflicts: []uint32{0, 1}}, // balanced
			{ID: 6, Priority: 40},                            // llm_inference
			{ID: 13, Priority: 50},                           // hpc
		},
	}
}

func profileDevice(t *testing.T, wpp *WorkloadPowerProfilesConfig) *ConfigurableDevice {
	t.Helper()
	return newTestDeviceWithConfig(t, &DeviceConfig{
		Power: &PowerConfig{
			EnforcedLimitMW:  400000,
			MinLimitMW:       100000,
			MaxLimitMW:       400000,
			WorkloadProfiles: wpp,
		},
	})
}

// maskBits expands a Mask255 back into the ids it names, so the assertions read
// as profile ids rather than as hex words.
func maskBits(m nvml.Mask255) []uint32 {
	var ids []uint32
	for i := range uint32(workloadPowerProfileMaxProfiles) {
		if m.Mask[i/32]&(1<<(i%32)) != 0 {
			ids = append(ids, i)
		}
	}
	return ids
}

// TestWorkloadProfiles_UnconfiguredDeclines is the pre-Blackwell case. The
// getters must decline rather than answer with an empty profile set, which
// nvidia-smi would render as a device that supports the feature and offers
// nothing.
func TestWorkloadProfiles_UnconfiguredDeclines(t *testing.T) {
	cases := []struct {
		name string
		cfg  *DeviceConfig
	}{
		{"no power section", &DeviceConfig{}},
		{"power without workload profiles", &DeviceConfig{Power: &PowerConfig{EnforcedLimitMW: 400000}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dev := newTestDeviceWithConfig(t, tc.cfg)

			_, ret := dev.WorkloadPowerProfileGetProfilesInfo()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)

			_, ret = dev.WorkloadPowerProfileGetCurrentProfiles()
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
		})
	}
}

func TestWorkloadProfiles_ProfilesInfoReportsSupportedSet(t *testing.T) {
	dev := profileDevice(t, blackwellProfiles())

	info, ret := dev.WorkloadPowerProfileGetProfilesInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{0, 1, 5, 6, 13}, maskBits(info.PerfProfilesMask))

	// Indexed by profile id: entry 5 is balanced, not the third supported
	// profile. nvidia-smi reads perfProfile[id], so this is what keeps its
	// rendered priorities attached to the right profile.
	for _, id := range []uint32{0, 1, 5, 6, 13} {
		require.Equal(t, id, info.PerfProfile[id].ProfileId, "entry %d", id)
	}
	require.Equal(t, uint32(30), info.PerfProfile[5].Priority)
	require.Equal(t, []uint32{0, 1}, maskBits(info.PerfProfile[5].ConflictingMask))

	// Unsupported ids stay zeroed, so a consumer that reads an entry the mask
	// does not name gets an obvious zero rather than another profile's data.
	require.Zero(t, info.PerfProfile[2].ProfileId)
	require.Zero(t, info.PerfProfile[2].Version)
	require.Zero(t, info.PerfProfile[254].Version)
}

// TestWorkloadProfiles_VersionsAreStamped covers the tags nvidia-smi checks
// before trusting the struct it was handed.
func TestWorkloadProfiles_VersionsAreStamped(t *testing.T) {
	dev := profileDevice(t, blackwellProfiles())

	info, ret := dev.WorkloadPowerProfileGetProfilesInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileProfilesInfo_v1{}, 1), info.Version)
	require.Equal(t, nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileInfo_v1{}, 1), info.PerfProfile[13].Version)

	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, nvml.STRUCT_VERSION(nvml.WorkloadPowerProfileCurrentProfiles_v1{}, 1), cur.Version)
}

// TestWorkloadProfiles_NothingRequestedByDefault is the fidelity case behind
// the default. Every real GB200 / GB300 / B200 capture in
// assertions/nvidiasmi/testdata/hardware reports N/A for both the requested and
// the enforced profiles, which is what empty masks render as. Preselecting a
// profile here would make `nvidia-smi -q -x` disagree with all of them.
func TestWorkloadProfiles_NothingRequestedByDefault(t *testing.T) {
	dev := profileDevice(t, blackwellProfiles())

	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{0, 1, 5, 6, 13}, maskBits(cur.PerfProfilesMask))
	require.Empty(t, maskBits(cur.RequestedProfilesMask))
	require.Empty(t, maskBits(cur.EnforcedProfilesMask))
}

func TestWorkloadProfiles_RequestedWithoutConflictAllEnforced(t *testing.T) {
	cfg := blackwellProfiles()
	cfg.Requested = []uint32{6, 13}
	dev := profileDevice(t, cfg)

	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{6, 13}, maskBits(cur.RequestedProfilesMask))
	require.Equal(t, []uint32{6, 13}, maskBits(cur.EnforcedProfilesMask))
}

// TestWorkloadProfiles_ConflictArbitratedByPriority covers the difference
// between requested and enforced, which is the whole reason NVML reports two
// masks: asking for mutually exclusive profiles is allowed, and the lower
// priority value wins.
func TestWorkloadProfiles_ConflictArbitratedByPriority(t *testing.T) {
	cases := []struct {
		name          string
		requested     []uint32
		wantRequested []uint32
		wantEnforced  []uint32
	}{
		{"max_p beats balanced", []uint32{0, 5}, []uint32{0, 5}, []uint32{0}},
		{"max_p beats max_q", []uint32{0, 1}, []uint32{0, 1}, []uint32{0}},
		{"max_q beats balanced", []uint32{1, 5}, []uint32{1, 5}, []uint32{1}},
		{"config order does not matter", []uint32{5, 1}, []uint32{1, 5}, []uint32{1}},
		{"three-way keeps the highest priority", []uint32{0, 1, 5}, []uint32{0, 1, 5}, []uint32{0}},
		{"conflict does not drop unrelated profiles", []uint32{1, 5, 6}, []uint32{1, 5, 6}, []uint32{1, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := blackwellProfiles()
			cfg.Requested = tc.requested
			dev := profileDevice(t, cfg)

			cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, tc.wantRequested, maskBits(cur.RequestedProfilesMask),
				"every requested profile is reported as requested even when it loses arbitration")
			require.Equal(t, tc.wantEnforced, maskBits(cur.EnforcedProfilesMask))
		})
	}
}

// TestWorkloadProfiles_RequestedMustBeSupported keeps a typo in a profile YAML
// from producing a requested profile the device never advertised.
func TestWorkloadProfiles_RequestedMustBeSupported(t *testing.T) {
	cfg := blackwellProfiles()
	cfg.Requested = []uint32{6, 9}
	dev := profileDevice(t, cfg)

	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{6}, maskBits(cur.RequestedProfilesMask))
	require.Equal(t, []uint32{6}, maskBits(cur.EnforcedProfilesMask))
}

// TestWorkloadProfiles_MaskCrossesElementBoundary pins the bit arithmetic. The
// mask is eight 32-bit words, so an id above 31 lands in a later word; getting
// this wrong silently reports the wrong profile.
func TestWorkloadProfiles_MaskCrossesElementBoundary(t *testing.T) {
	dev := profileDevice(t, &WorkloadPowerProfilesConfig{
		Supported: []WorkloadPowerProfileConfig{{ID: 0}, {ID: 31}, {ID: 33}, {ID: 254}},
	})

	info, ret := dev.WorkloadPowerProfileGetProfilesInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{0, 31, 33, 254}, maskBits(info.PerfProfilesMask))
	require.Equal(t, uint32(1<<0|1<<31), info.PerfProfilesMask.Mask[0])
	require.Equal(t, uint32(1<<1), info.PerfProfilesMask.Mask[1])
	require.Equal(t, uint32(1<<30), info.PerfProfilesMask.Mask[7])
}

// TestWorkloadProfiles_OutOfRangeIDsIgnored keeps an id that has no bit in a
// 255-bit mask from panicking or aliasing onto a valid profile.
func TestWorkloadProfiles_OutOfRangeIDsIgnored(t *testing.T) {
	dev := profileDevice(t, &WorkloadPowerProfilesConfig{
		Supported: []WorkloadPowerProfileConfig{{ID: 6}, {ID: 255}, {ID: 9000}},
		Requested: []uint32{255},
	})

	info, ret := dev.WorkloadPowerProfileGetProfilesInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, []uint32{6}, maskBits(info.PerfProfilesMask))

	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, maskBits(cur.RequestedProfilesMask))
}

// TestWorkloadProfiles_SupportedButEmpty separates "the feature is absent" from
// "the feature is present and lists nothing", which are different answers to
// nvidia-smi.
func TestWorkloadProfiles_SupportedButEmpty(t *testing.T) {
	dev := profileDevice(t, &WorkloadPowerProfilesConfig{})

	info, ret := dev.WorkloadPowerProfileGetProfilesInfo()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, maskBits(info.PerfProfilesMask))

	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Empty(t, maskBits(cur.PerfProfilesMask))
}

// --- Requested profile setters ---

// requestedMask reads back what the device now reports as requested, which is
// the only way a consumer can observe a write landing.
func requestedMask(t *testing.T, dev *ConfigurableDevice) []uint32 {
	t.Helper()
	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	return maskBits(cur.RequestedProfilesMask)
}

func enforcedMask(t *testing.T, dev *ConfigurableDevice) []uint32 {
	t.Helper()
	cur, ret := dev.WorkloadPowerProfileGetCurrentProfiles()
	require.Equal(t, nvml.SUCCESS, ret)
	return maskBits(cur.EnforcedProfilesMask)
}

// TestWorkloadProfiles_SetAddsToRequested is the read-after-write the whole
// setter exists for: `nvidia-smi power-profiles -sr` must be observable through
// -gr, and SET adds rather than replaces.
func TestWorkloadProfiles_SetAddsToRequested(t *testing.T) {
	persistSetterWrites(t)
	dev := profileDevice(t, blackwellProfiles())

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{6})))
	require.Equal(t, []uint32{6}, requestedMask(t, dev))

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{13})))
	require.Equal(t, []uint32{6, 13}, requestedMask(t, dev))
}

func TestWorkloadProfiles_ClearRemovesFromRequested(t *testing.T) {
	persistSetterWrites(t)
	dev := profileDevice(t, blackwellProfiles())

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{6, 13})))
	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_CLEAR, workloadProfileMask([]uint32{6})))
	require.Equal(t, []uint32{13}, requestedMask(t, dev))

	// Clearing something that was never requested is a no-op, not an error:
	// the mask names profiles to remove, and removing nothing removes nothing.
	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_CLEAR, workloadProfileMask([]uint32{6})))
	require.Equal(t, []uint32{13}, requestedMask(t, dev))
}

func TestWorkloadProfiles_SetAndOverwriteReplaces(t *testing.T) {
	persistSetterWrites(t)
	dev := profileDevice(t, blackwellProfiles())

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{6, 13})))
	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET_AND_OVERWRITE, workloadProfileMask([]uint32{0})))
	require.Equal(t, []uint32{0}, requestedMask(t, dev))
}

// TestWorkloadProfiles_SetOverridesConfiguredRequest pins the precedence: a
// write outranks the profile's configured request, the way a real driver holds
// a runtime request until it unloads.
func TestWorkloadProfiles_SetOverridesConfiguredRequest(t *testing.T) {
	persistSetterWrites(t)
	cfg := blackwellProfiles()
	cfg.Requested = []uint32{6}
	dev := profileDevice(t, cfg)
	require.Equal(t, []uint32{6}, requestedMask(t, dev))

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_CLEAR, workloadProfileMask([]uint32{6})))
	require.Empty(t, requestedMask(t, dev))

	// An empty override must stay distinguishable from "never written", or
	// clearing the last profile would fall back to the configured request.
	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{13})))
	require.Equal(t, []uint32{13}, requestedMask(t, dev))
}

// TestWorkloadProfiles_SetArbitratesEnforced is what separates the two masks at
// runtime: requesting conflicting profiles is allowed and both are reported as
// requested, but only the higher-priority one is enforced.
func TestWorkloadProfiles_SetArbitratesEnforced(t *testing.T) {
	persistSetterWrites(t)
	dev := profileDevice(t, blackwellProfiles())

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{5, 6})))
	require.Equal(t, []uint32{5, 6}, requestedMask(t, dev))
	require.Equal(t, []uint32{5, 6}, enforcedMask(t, dev))

	// max_p outranks balanced, so balanced stays requested but stops being
	// enforced.
	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{0})))
	require.Equal(t, []uint32{0, 5, 6}, requestedMask(t, dev))
	require.Equal(t, []uint32{0, 6}, enforcedMask(t, dev))
}

// TestWorkloadProfiles_SetRejectsUnsupported keeps a consumer from requesting a
// profile the board never advertised. Unlike the config path, which drops a bad
// id quietly, a live caller gets told.
func TestWorkloadProfiles_SetRejectsUnsupported(t *testing.T) {
	dev := profileDevice(t, blackwellProfiles())

	for _, op := range []nvml.PowerProfileOperation{
		nvml.POWER_PROFILE_OPERATION_SET,
		nvml.POWER_PROFILE_OPERATION_CLEAR,
		nvml.POWER_PROFILE_OPERATION_SET_AND_OVERWRITE,
	} {
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
			dev.WorkloadPowerProfileUpdateProfiles(op, workloadProfileMask([]uint32{9})),
			"op %d should refuse an unadvertised profile", op)
	}
	require.Empty(t, requestedMask(t, dev), "a refused write must not land")
}

func TestWorkloadProfiles_UpdateRejectsUnknownOperation(t *testing.T) {
	dev := profileDevice(t, blackwellProfiles())

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT,
		dev.WorkloadPowerProfileUpdateProfiles(
			nvml.POWER_PROFILE_OPERATION_MAX, workloadProfileMask([]uint32{6})))
	require.Empty(t, requestedMask(t, dev))
}

// TestWorkloadProfiles_UpdateOnUnconfiguredDevice keeps the setters consistent
// with the getters: a device that does not model the feature declines the write
// rather than accepting one nothing can read back.
func TestWorkloadProfiles_UpdateOnUnconfiguredDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Power: &PowerConfig{EnforcedLimitMW: 400000}})

	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{6})))
}

// TestWorkloadProfiles_EmptyMaskIsANoOp covers the degenerate write. It must not
// be mistaken for "clear everything", which is what SET_AND_OVERWRITE is for.
func TestWorkloadProfiles_EmptyMaskIsANoOp(t *testing.T) {
	persistSetterWrites(t)
	dev := profileDevice(t, blackwellProfiles())
	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{6})))

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, nvml.Mask255{}))
	require.Equal(t, []uint32{6}, requestedMask(t, dev))

	require.Equal(t, nvml.SUCCESS, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET_AND_OVERWRITE, nvml.Mask255{}))
	require.Empty(t, requestedMask(t, dev))
}

func TestWorkloadProfiles_UpdateLostDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Power:   &PowerConfig{EnforcedLimitMW: 400000, WorkloadProfiles: blackwellProfiles()},
		Failure: &FailureInjectionConfig{Mode: FailureModeLost},
	})

	require.Equal(t, nvml.ERROR_GPU_IS_LOST, dev.WorkloadPowerProfileUpdateProfiles(
		nvml.POWER_PROFILE_OPERATION_SET, workloadProfileMask([]uint32{6})))
}

// Concurrent updates are not covered here. SET and CLEAR rebuild the request
// from its current value, but the fold now runs inside the writer's lock, so
// "two racing writers must not lose an update" is a property of the writer:
// mockctl's TestUpdateWorkloadProfiles_ConcurrentWritersDoNotLoseUpdates pins
// it against the implementation that actually takes the lock.

// TestWorkloadProfiles_LostDevice keeps the getters consistent with the other
// power reads under injected failure.
func TestWorkloadProfiles_LostDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Power:   &PowerConfig{EnforcedLimitMW: 400000, WorkloadProfiles: blackwellProfiles()},
		Failure: &FailureInjectionConfig{Mode: FailureModeLost},
	})

	_, ret := dev.WorkloadPowerProfileGetProfilesInfo()
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, ret)
}
