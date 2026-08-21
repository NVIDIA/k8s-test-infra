// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// The five field ids nvidia-smi 580 reads for the "Clocks Event Reasons
// Counters" block, in the order it renders them. Keeping the pairing in one
// place means a test that sets one cause can assert on the other four.
var throttleCounterFields = []struct {
	name    string
	fieldID uint32
}{
	{"SW Power Capping", fiPerfPolicyPower},
	{"Sync Boost", fiPerfPolicySyncBoost},
	{"SW Thermal Slowdown", fiClocksEventSwThermSlowdown},
	{"HW Thermal Slowdown", fiClocksEventHwThermSlowdown},
	{"HW Power Braking", fiClocksEventHwPowerBrake},
}

// A healthy profile has to answer these counters, not decline them: nvidia-smi
// renders a per-field NOT_SUPPORTED as N/A, and "never throttled" is 0, not
// "unknown". This holds with no clocks_throttle_reasons block at all, which is
// what most shipped profiles have.
func TestThrottleCounters_HealthyDeviceReportsZeroNotUnsupported(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "blackwell"})

	for _, f := range throttleCounterFields {
		vt, val, ret := dev.GetFieldValue(f.fieldID, 0)
		require.Equal(t, nvml.SUCCESS, ret, "%s (field %d) must be supported", f.name, f.fieldID)
		require.Equal(t, FieldValueUint64, vt, "%s value type", f.name)
		require.Zero(t, val, "%s on a healthy device", f.name)
	}
}

// Each cause is configured and read independently: a profile that has been
// power-capped has not also been thermally throttled, and the field ids must
// not alias onto one another.
func TestThrottleCounters_ConfiguredCauseIsReportedInIsolation(t *testing.T) {
	for _, target := range throttleCounterFields {
		t.Run(target.name, func(t *testing.T) {
			counters := &ThrottleCountersConfig{}
			switch target.fieldID {
			case fiPerfPolicyPower:
				counters.SWPowerCapUS = 39595
			case fiPerfPolicySyncBoost:
				counters.SyncBoostUS = 39595
			case fiClocksEventSwThermSlowdown:
				counters.SWThermalSlowdownUS = 39595
			case fiClocksEventHwThermSlowdown:
				counters.HWThermalSlowdownUS = 39595
			case fiClocksEventHwPowerBrake:
				counters.HWPowerBrakeSlowdownUS = 39595
			}
			dev := newTestDeviceWithConfig(t, &DeviceConfig{
				Architecture:          "blackwell",
				ClocksThrottleReasons: &ClocksThrottleReasonsConfig{Counters: counters},
			})

			for _, f := range throttleCounterFields {
				_, val, ret := dev.GetFieldValue(f.fieldID, 0)
				require.Equal(t, nvml.SUCCESS, ret, "%s (field %d)", f.name, f.fieldID)
				if f.fieldID == target.fieldID {
					// Counters are nanoseconds on the wire, microseconds in config.
					require.Equal(t, uint64(39595*1000), val, "%s", f.name)
					continue
				}
				require.Zero(t, val, "%s must stay zero while %s is set", f.name, target.name)
			}
		})
	}
}

// The three slowdown counters sit where the simulated driver puts them, not
// where the vendored CUDA 13 header does. Pinning the numbers keeps a header
// bump from silently moving them: the ids below are what nvidia-smi 580.65.06
// asks for, and 269-271 are power-smoothing ramp fields to that driver, so
// answering them with a throttle counter would fabricate readings for an
// unrelated feature.
func TestThrottleCounters_FieldIDsMatchTheSimulatedDriver(t *testing.T) {
	require.Equal(t, uint32(251), uint32(fiClocksEventSwThermSlowdown))
	require.Equal(t, uint32(252), uint32(fiClocksEventHwThermSlowdown))
	require.Equal(t, uint32(253), uint32(fiClocksEventHwPowerBrake))

	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "blackwell",
		ClocksThrottleReasons: &ClocksThrottleReasonsConfig{
			Counters: &ThrottleCountersConfig{SWThermalSlowdownUS: 1},
		},
	})
	// nvidia-smi sweeps the power-smoothing range with a profile id in scopeId.
	// Every entry in it must still decline.
	for fieldID := uint32(256); fieldID <= 273; fieldID++ {
		_, _, ret := dev.GetFieldValue(fieldID, 5)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret,
			"field %d belongs to power smoothing, not to the throttle counters", fieldID)
	}
}

// NVML_FI_DEV_PERF_POLICY_THERMAL is the older id for the same counter
// NVML_FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN carries. DCGM-era readers
// may ask for either, so both must resolve to the same accrual.
func TestThrottleCounters_ThermalFieldIDsAlias(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "blackwell",
		ClocksThrottleReasons: &ClocksThrottleReasonsConfig{
			Counters: &ThrottleCountersConfig{SWThermalSlowdownUS: 1234},
		},
	})

	_, viaPerfPolicy, ret := dev.GetFieldValue(fiPerfPolicyThermal, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	_, viaClocksEvent, ret := dev.GetFieldValue(fiClocksEventSwThermSlowdown, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(1234*1000), viaPerfPolicy)
	require.Equal(t, viaPerfPolicy, viaClocksEvent)
}

// Throttling is a property of one board, not of a node: a tray where one GPU
// has been power-capped and its neighbours have not is the case worth
// simulating, so the counters have to be settable per device.
func TestThrottleCounters_ConfigurablePerDevice(t *testing.T) {
	e := NewEngine(&Config{
		NumDevices:    2,
		DriverVersion: "550.163",
		YAMLConfig: &YAMLConfig{
			Version: "1.0",
			System:  SystemConfig{DriverVersion: "550.163", NumDevices: 2},
			DeviceDefaults: DeviceConfig{
				Architecture:          "blackwell",
				ClocksThrottleReasons: &ClocksThrottleReasonsConfig{GPUIdle: true},
			},
			Devices: []DeviceOverride{{
				Index: 1,
				DeviceConfig: DeviceConfig{ClocksThrottleReasons: &ClocksThrottleReasonsConfig{
					Counters: &ThrottleCountersConfig{SWPowerCapUS: 39595},
				}},
			}},
		},
	})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })

	for index, wantUS := range map[int]uint64{0: 0, 1: 39595} {
		handle, ret := e.DeviceGetHandleByIndex(index)
		require.Equal(t, nvml.SUCCESS, ret)
		dev, ok := e.LookupDevice(handle).(*ConfigurableDevice)
		require.True(t, ok)

		_, val, ret := dev.GetFieldValue(fiPerfPolicyPower, 0)
		require.Equal(t, nvml.SUCCESS, ret)
		require.Equal(t, wantUS*1000, val, "GPU %d SW power capping", index)
	}
}

// A profile that reports a throttle reason as Active cannot also report that
// the cause has never cost the GPU any time. The counter accrues while the
// flag is set, so the flags and the counters stay consistent.
func TestThrottleCounters_AccrueWhileReasonActive(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "blackwell",
		ClocksThrottleReasons: &ClocksThrottleReasonsConfig{
			SWPowerCap: true,
			Counters:   &ThrottleCountersConfig{SWPowerCapUS: 1000},
		},
	})

	clock := time.Now()
	dev.throttleAccrual().now = func() time.Time { return clock }

	// First read opens the accrual interval; the reported value is still the
	// configured baseline because no time has passed yet.
	_, val, ret := dev.GetFieldValue(fiPerfPolicyPower, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(1000*1000), val, "baseline before any time passes")

	clock = clock.Add(2 * time.Second)
	_, val, ret = dev.GetFieldValue(fiPerfPolicyPower, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(1000*1000)+uint64(2*time.Second), val,
		"an active SW power cap must accrue the elapsed time")

	// Causes whose flag is clear stay at their baseline of zero.
	_, val, ret = dev.GetFieldValue(fiClocksEventHwThermSlowdown, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Zero(t, val, "an inactive cause must not accrue")
}

// Accrual is monotonic across the flag clearing: the time already spent
// throttled is what a post-mortem reads, so it must not be given back.
func TestThrottleCounters_AccrualSurvivesReasonClearing(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture:          "blackwell",
		ClocksThrottleReasons: &ClocksThrottleReasonsConfig{SWPowerCap: true},
	})

	clock := time.Now()
	accrual := dev.throttleAccrual()
	accrual.now = func() time.Time { return clock }

	_, _, ret := dev.GetFieldValue(fiPerfPolicyPower, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	clock = clock.Add(3 * time.Second)
	_, val, ret := dev.GetFieldValue(fiPerfPolicyPower, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(3*time.Second), val)

	// Clear the reason the way a runtime override would, then let more time
	// pass: the accrued 3s stays, the idle time is not added.
	cleared := *accrual.device.cfg()
	cleared.ClocksThrottleReasons = &ClocksThrottleReasonsConfig{}
	dev.effective.Store(&cleared)

	clock = clock.Add(10 * time.Second)
	_, val, ret = dev.GetFieldValue(fiPerfPolicyPower, 0)
	require.Equal(t, nvml.SUCCESS, ret)
	require.Equal(t, uint64(3*time.Second), val, "cleared reason must neither reset nor keep accruing")
}

// nvmlDeviceGetViolationStatus is the deprecated getter for the same state.
// It must answer per policy rather than reporting one blanket verdict.
func TestGetViolationStatus_PerPolicyAccrual(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{
		Architecture: "blackwell",
		ClocksThrottleReasons: &ClocksThrottleReasonsConfig{
			Counters: &ThrottleCountersConfig{
				SWPowerCapUS:        11,
				SWThermalSlowdownUS: 22,
				SyncBoostUS:         33,
			},
		},
	})

	for _, tt := range []struct {
		policy nvml.PerfPolicyType
		wantNs uint64
	}{
		{nvml.PERF_POLICY_POWER, 11 * 1000},
		{nvml.PERF_POLICY_THERMAL, 22 * 1000},
		{nvml.PERF_POLICY_SYNC_BOOST, 33 * 1000},
	} {
		vt, ret := dev.GetViolationStatus(tt.policy)
		require.Equal(t, nvml.SUCCESS, ret, "policy %d", tt.policy)
		require.Equal(t, tt.wantNs, vt.ViolationTime, "policy %d violation time", tt.policy)
		require.NotZero(t, vt.ReferenceTime, "policy %d reference time", tt.policy)
	}
}

// Policies the mock models no counter for decline rather than answering zero:
// a fabricated zero is indistinguishable from "never throttled", and these
// limiters do not exist on the hardware the profiles describe.
// The limiters the mock does not model are still real policies with their own
// field ids, so they answer zero. NOT_SUPPORTED would say the driver cannot
// tell whether the limiter ever fired, which is the same misreading that made
// these counters render as N/A.
func TestGetViolationStatus_UnmodeledPoliciesReportZero(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "blackwell"})

	for _, policy := range []nvml.PerfPolicyType{
		nvml.PERF_POLICY_BOARD_LIMIT,
		nvml.PERF_POLICY_LOW_UTILIZATION,
		nvml.PERF_POLICY_RELIABILITY,
		nvml.PERF_POLICY_TOTAL_BASE_CLOCKS,
	} {
		vt, ret := dev.GetViolationStatus(policy)
		require.Equal(t, nvml.SUCCESS, ret, "policy %d", policy)
		require.Zero(t, vt.ViolationTime, "policy %d must report no accrued time, not decline", policy)
		require.NotZero(t, vt.ReferenceTime, "policy %d must still timestamp the reading", policy)
	}
}

// TOTAL_APP_CLOCKS is the one policy nvml.h gives no field id, marking it
// "DEPRECATED, Do not use", so it is the one that declines.
func TestGetViolationStatus_DeprecatedTotalAppClocksDeclines(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "blackwell"})

	_, ret := dev.GetViolationStatus(nvml.PERF_POLICY_TOTAL_APP_CLOCKS)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}

// A value outside the enum, including its 6-9 gap, is not a policy any driver
// answers.
func TestGetViolationStatus_RejectsPoliciesOutsideTheEnum(t *testing.T) {
	dev := newTestDeviceWithConfig(t, &DeviceConfig{Architecture: "blackwell"})

	for _, policy := range []nvml.PerfPolicyType{7, nvml.PERF_POLICY_COUNT, -1} {
		_, ret := dev.GetViolationStatus(policy)
		require.Equal(t, nvml.ERROR_INVALID_ARGUMENT, ret, "policy %d", policy)
	}
}
