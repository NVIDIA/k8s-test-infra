// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

// vrCaptureDump is the NVML recording of the real Vera Rubin tray.
var vrCaptureDump = filepath.Join("..", "..", "..", "..", "tests", "hardware-captures", "vr-nvl72", "node-a", "nvml-dump.json.gz")

// nvlink5ErrorFields are the NVLink5 error counters a fields-only driver keeps
// answering, so they are compared with the capture too.
var nvlink5ErrorFields = []uint32{
	fiNvlinkCountMalformedPacketErrors, fiNvlinkCountBufferOverrunErrors,
	fiNvlinkCountRcvErrors, fiNvlinkCountRcvRemoteErrors,
	fiNvlinkCountRcvGeneralErrors, fiNvlinkCountLocalLinkIntegrityErrors,
	fiNvlinkCountXmitDiscards, fiNvlinkCountLinkRecoverySuccessfulEvents,
	fiNvlinkCountLinkRecoveryFailedEvents, fiNvlinkCountLinkRecoveryEvents,
	fiNvlinkCountEffectiveErrors, fiNvlinkCountSymbolErrors,
}

// captureReturns reads the return code of every recorded call, per GPU index.
func captureReturns(t *testing.T, path string) map[int]map[string]string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	var dump struct {
		Devices []struct {
			Index int `json:"index"`
			Calls map[string]struct {
				Ret string `json:"ret"`
			} `json:"calls"`
		} `json:"devices"`
	}
	require.NoError(t, json.NewDecoder(zr).Decode(&dump))
	out := map[int]map[string]string{}
	for _, dev := range dump.Devices {
		out[dev.Index] = map[string]string{}
		for call, rec := range dev.Calls {
			out[dev.Index][call] = rec.Ret
		}
	}
	return out
}

var (
	errorCounterCall = regexp.MustCompile(`^GetNvLinkErrorCounter\((\d+),(\d+)\)$`)
	utilCounterCall  = regexp.MustCompile(`^GetNvLinkUtilizationCounter\((\d+),(\d+)\)$`)
	fieldValueCall   = regexp.MustCompile(`^GetFieldValues\(id=(\d+)\)$`)
)

// mockReturn answers a recorded NVLink telemetry call on d, or reports false
// for a call outside the NVLink telemetry this test covers.
func mockReturn(t *testing.T, d *ConfigurableDevice, call string) (nvml.Return, bool) {
	t.Helper()
	atoi := func(s string) int {
		n, err := strconv.Atoi(s)
		require.NoError(t, err)
		return n
	}
	if m := errorCounterCall.FindStringSubmatch(call); m != nil {
		_, ret := d.GetNvLinkErrorCounter(atoi(m[1]), nvml.NvLinkErrorCounter(atoi(m[2])))
		return ret, true
	}
	if m := utilCounterCall.FindStringSubmatch(call); m != nil {
		_, _, ret := d.GetNvLinkUtilizationCounter(atoi(m[1]), atoi(m[2]))
		return ret, true
	}
	if m := fieldValueCall.FindStringSubmatch(call); m != nil {
		id := uint32(atoi(m[1]))
		covered := isLegacyNVLinkField(id)
		for _, f := range nvlink5ErrorFields {
			covered = covered || id == f
		}
		if !covered {
			return 0, false
		}
		_, _, ret := d.GetFieldValue(id, 0)
		return ret, true
	}
	return 0, false
}

// Every NVLink counter getter, legacy NVLink field and NVLink5 error field the
// real Vera Rubin tray was asked answers with the same code in vrnvl72: the
// getters and legacy fields NOT_SUPPORTED, links past the last one included,
// and the NVLink5 error fields SUCCESS.
func TestNVLinkTelemetry_MatchesTheRealTray(t *testing.T) {
	t.Parallel()
	yc, err := LoadYAMLConfig(filepath.Join("..", "configs", "mock-nvml-config-vrnvl72.yaml"))
	require.NoError(t, err)
	e := NewEngine(&Config{NumDevices: len(yc.Devices), YAMLConfig: yc})
	require.Equal(t, nvml.SUCCESS, e.Init())
	t.Cleanup(func() { _ = e.Shutdown() })

	names := map[string]nvml.Return{
		"SUCCESS":                nvml.SUCCESS,
		"ERROR_NOT_SUPPORTED":    nvml.ERROR_NOT_SUPPORTED,
		"ERROR_INVALID_ARGUMENT": nvml.ERROR_INVALID_ARGUMENT,
	}
	compared := 0
	for index, calls := range captureReturns(t, vrCaptureDump) {
		handle, ret := e.DeviceGetHandleByIndex(index)
		require.Equal(t, nvml.SUCCESS, ret)
		d := e.LookupConfigurableDevice(handle)
		for call, want := range calls {
			got, ok := mockReturn(t, d, call)
			if !ok {
				continue
			}
			wantRet, known := names[want]
			require.True(t, known, "GPU %d %s: unexpected code %s in the capture", index, call, want)
			require.Equal(t, wantRet, got, "GPU %d %s", index, call)
			compared++
		}
	}
	// 4 GPUs: 200 error and 80 utilization counter calls, 16 legacy and 12
	// NVLink5 error fields each.
	require.Equal(t, 4*(200+80+16+12), compared)
}

// Drivers before 615 keep answering the getters and the legacy fields, so the
// 580 profiles do not change; a 580 profile moved to a 615 driver loses them.
func TestNVLinkTelemetry_TheDriverDecides(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		profile, driver string
		want            nvml.Return
	}{
		{"a100", "", nvml.SUCCESS},
		{"h100", "", nvml.SUCCESS},
		{"b200", "", nvml.SUCCESS},
		{"gb200", "", nvml.SUCCESS},
		{"gb300", "", nvml.SUCCESS},
		{"gb300", "615.71.09", nvml.ERROR_NOT_SUPPORTED},
	} {
		t.Run(tc.profile+"/"+tc.driver, func(t *testing.T) {
			t.Parallel()
			d := shippedProfileDevice(t, tc.profile, tc.driver)
			_, ret := d.GetNvLinkErrorCounter(0, nvml.NVLINK_ERROR_DL_CRC_FLIT)
			require.Equal(t, tc.want, ret, "GetNvLinkErrorCounter")
			_, _, ret = d.GetNvLinkUtilizationCounter(0, 0)
			require.Equal(t, tc.want, ret, "GetNvLinkUtilizationCounter")
			for _, id := range []uint32{fiNvlinkSpeedMbpsL0, fiNvlinkSpeedMbpsCommon, fiNvlinkErrorDlCrc} {
				_, _, ret := d.GetFieldValue(id, 0)
				require.Equal(t, tc.want, ret, "field %d", id)
			}
		})
	}
}

// On a fields-only driver an NVLinkError injection still shows: the NVLink5
// error fields climb while the getter and the data-link error fields answer
// NOT_SUPPORTED.
func TestNVLinkTelemetry_InjectionReachesTheFields(t *testing.T) {
	cd := injectionDevice(t, 4, &NVLinkErrorInjectionConfig{Rate: 100}, 10*time.Second)
	cd.driverVersion = "615.71.09"

	_, ret := cd.GetNvLinkErrorCounter(0, nvml.NVLINK_ERROR_DL_CRC_FLIT)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	_, _, ret = cd.GetFieldValue(fiNvlinkErrorDlCrc, 0)
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
	for _, id := range nvlink5ErrorFields {
		_, v, ret := cd.GetFieldValue(id, 0)
		require.Equal(t, nvml.SUCCESS, ret, "field %d", id)
		require.Equal(t, uint64(1000), v, "field %d: 100 err/s * 10s", id)
	}
}

// A MIG device reports NVLink telemetry like its GPU: the driver decides which
// calls exist, for every handle it serves.
func TestNVLinkTelemetry_MigDeviceFollowsItsGPU(t *testing.T) {
	t.Parallel()
	dev := newTestDeviceWithConfig(t, a100MIGConfig())
	dev.driverVersion = "615.71.09"
	enableMIG(t, dev)
	createSpanningCI(t, createOneSliceGI(t, dev))
	handle, ret := dev.GetMigDeviceHandleByIndex(0)
	require.Equal(t, nvml.SUCCESS, ret)
	mig, ok := handle.(*ConfigurableDevice)
	require.True(t, ok)

	for name, d := range map[string]*ConfigurableDevice{"GPU": dev, "MIG device": mig} {
		_, ret := d.GetNvLinkErrorCounter(0, nvml.NVLINK_ERROR_DL_CRC_FLIT)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "%s GetNvLinkErrorCounter", name)
		_, _, ret = d.GetNvLinkUtilizationCounter(0, 0)
		require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret, "%s GetNvLinkUtilizationCounter", name)
	}
}
