// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"sync"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func powerCappableDevice(t *testing.T) *ConfigurableDevice {
	t.Helper()
	return newTestDeviceWithConfig(t, &DeviceConfig{
		Power: &PowerConfig{
			DefaultLimitMW:  400000,
			EnforcedLimitMW: 400000,
			MinLimitMW:      100000,
			MaxLimitMW:      400000,
			CurrentDrawMW:   250000,
		},
	})
}

// TestSetPowerManagementLimit_RoundTrips covers the read-after-write a power
// capping controller performs to confirm its cap landed. Answering SUCCESS
// while the getters keep reporting the old cap is worse than declining.
func TestSetPowerManagementLimit_RoundTrips(t *testing.T) {
	dev := powerCappableDevice(t)

	require.Equal(t, nvml.SUCCESS, dev.SetPowerManagementLimit(250000))

	limit, ret := dev.GetPowerManagementLimit()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(250000), limit)

	enforced, ret := dev.GetEnforcedPowerLimit()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(250000), enforced,
		"the enforced limit is what nvidia-smi renders and must follow the cap")
}

// TestSetPowerManagementLimit_LeavesDefaultLimit pins the one getter that must
// not move: the default limit is a board property, and `nvidia-smi -pl` uses it
// to offer a reset target.
func TestSetPowerManagementLimit_LeavesDefaultLimit(t *testing.T) {
	dev := powerCappableDevice(t)

	require.Equal(t, nvml.SUCCESS, dev.SetPowerManagementLimit(250000))

	def, ret := dev.GetPowerManagementDefaultLimit()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(400000), def)
}

func TestSetPowerManagementLimit_RejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name  string
		limit uint32
	}{
		{"below min", 99999},
		{"above max", 400001},
		{"zero", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dev := powerCappableDevice(t)

			require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetPowerManagementLimit(tc.limit))

			limit, ret := dev.GetPowerManagementLimit()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, uint32(400000), limit, "a rejected cap must not be applied")
		})
	}
}

func TestSetPowerManagementLimit_AcceptsConstraintBounds(t *testing.T) {
	for _, limit := range []uint32{100000, 400000} {
		dev := powerCappableDevice(t)
		require.Equal(t, nvml.SUCCESS, dev.SetPowerManagementLimit(limit),
			"the constraints reported by GetPowerManagementLimitConstraints are inclusive")
	}
}

// TestSetPowerManagementLimit_NoPowerConfig keeps the setter's support signal
// aligned with the getters': a device with no power section models a part that
// cannot report or set a cap.
func TestSetPowerManagementLimit_NoPowerConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{})

	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, dev.SetPowerManagementLimit(250000))
}

// TestSetPowerManagementLimit_UnconstrainedConfig covers a profile that lists a
// limit but no min/max. Real hardware always has constraints, so rather than
// inventing bounds the setter accepts any non-zero value.
func TestSetPowerManagementLimit_UnconstrainedConfig(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Power: &PowerConfig{EnforcedLimitMW: 400000},
	})

	require.Equal(t, nvml.SUCCESS, dev.SetPowerManagementLimit(250000))

	limit, ret := dev.GetPowerManagementLimit()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(250000), limit)
}

func TestSetPowerManagementLimitV2_RoundTripsGPUScope(t *testing.T) {
	dev := powerCappableDevice(t)

	ret := dev.SetPowerManagementLimit_v2(&nvml.PowerValue_v2{
		PowerScope:   nvml.POWER_SCOPE_GPU,
		PowerValueMw: 250000,
	})
	require.Equal(t, nvml.SUCCESS, ret)

	limit, ret := dev.GetPowerManagementLimit()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint32(250000), limit)
}

// TestSetPowerManagementLimitV2_UnmodelledScopes guards the scopes the mock has
// no separate budget for. Silently folding a module-wide or memory cap into the
// GPU limit would let a consumer believe it capped something it did not.
func TestSetPowerManagementLimitV2_UnmodelledScopes(t *testing.T) {
	cases := []struct {
		name  string
		scope uint8
		want  nvml.Return
	}{
		{"module", nvml.POWER_SCOPE_MODULE, nvml.ERROR_NOT_SUPPORTED},
		{"memory", nvml.POWER_SCOPE_MEMORY, nvml.ERROR_NOT_SUPPORTED},
		{"gpu base", nvml.POWER_SCOPE_GPU_BASE, nvml.ERROR_NOT_SUPPORTED},
		{"out of range", nvml.POWER_SCOPE_COUNT, nvml.ERROR_INVALID_ARGUMENT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dev := powerCappableDevice(t)

			ret := dev.SetPowerManagementLimit_v2(&nvml.PowerValue_v2{
				PowerScope:   tc.scope,
				PowerValueMw: 250000,
			})
			require.Equal(t, tc.want, ret)

			limit, ret := dev.GetPowerManagementLimit()
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, uint32(400000), limit)
		})
	}
}

func TestSetPowerManagementLimitV2_NilValue(t *testing.T) {
	dev := powerCappableDevice(t)

	require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, dev.SetPowerManagementLimit_v2(nil))
}

// TestSetPowerManagementLimit_VisibleThroughFieldValues covers DCGM's route to
// the cap. It reads NVML_FI_DEV_POWER_CURRENT_LIMIT rather than calling the
// getter, so a cap that only moved the getter would leave every DCGM power
// limit metric reporting the pre-cap value.
func TestSetPowerManagementLimit_VisibleThroughFieldValues(t *testing.T) {
	dev := powerCappableDevice(t)

	require.Equal(t, nvml.SUCCESS, dev.SetPowerManagementLimit(250000))

	for _, fieldID := range []uint32{fiPowerCurrentLimit, fiPowerRequestedLimit} {
		_, val, ret, handled := dev.powerFieldValue(fieldID)
		require.True(t, handled)
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, uint64(250000), val, "field %d", fieldID)
	}

	// The default limit is a separate field and must keep reporting the board
	// value, which is what a consumer resets the cap back to.
	_, val, ret, handled := dev.powerFieldValue(fiPowerDefaultLimit)
	require.True(t, handled)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(400000), val)
}

// TestSetPowerManagementLimit_LostDevice pins the setter to the same injected
// failure the getters honour: a GPU that has fallen off the bus cannot accept a
// new cap.
func TestSetPowerManagementLimit_LostDevice(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Power: &PowerConfig{
			EnforcedLimitMW: 400000,
			MinLimitMW:      100000,
			MaxLimitMW:      400000,
		},
		Failure: &FailureInjectionConfig{Mode: FailureModeLost},
	})

	require.Equal(t, nvml.ERROR_GPU_IS_LOST, dev.SetPowerManagementLimit(250000))
}

// TestSetPowerManagementLimit_ConcurrentSetAndGet exists because the limit is
// mutable driver state read by every power getter, so it must not be a plain
// field: consumers poll power while a controller writes the cap.
func TestSetPowerManagementLimit_ConcurrentSetAndGet(t *testing.T) {
	dev := powerCappableDevice(t)

	const workers = 4
	rets := make([]nvml.Return, 2*workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			rets[i] = dev.SetPowerManagementLimit(uint32(100000 + i*1000))
		}()
		go func() {
			defer wg.Done()
			_, rets[workers+i] = dev.GetPowerManagementLimit()
		}()
	}
	wg.Wait()

	for _, ret := range rets {
		require.Equal(t, nvml.SUCCESS, ret)
	}

	// Whichever writer landed last, the cap must be one of the offered values
	// rather than a torn read.
	limit, ret := dev.GetPowerManagementLimit()
	require.Equal(t, nvml.SUCCESS, ret)
	require.Contains(t, []uint32{100000, 101000, 102000, 103000}, limit)
}
