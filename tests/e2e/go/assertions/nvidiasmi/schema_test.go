// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadFixture reads a captured `nvidia-smi -q -x` document from testdata. See
// testdata/README.md for how the captures were taken.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err, "read fixture %s", name)
	return string(body)
}

func TestReading_DistinguishesAbsentUnsupportedFailedAndValue(t *testing.T) {
	tests := []struct {
		name        string
		raw         reading
		present     bool
		unsupported bool
		failed      bool
		want        int
		wantOK      bool
	}{
		{name: "absent", raw: ""},
		{name: "unsupported", raw: "N/A", present: true, unsupported: true},
		{name: "lost", raw: "GPU is lost", present: true, failed: true},
		{name: "unknown error", raw: "Unknown Error", present: true, failed: true},
		{name: "celsius", raw: "92 C", present: true, want: 92, wantOK: true},
		{name: "negative tlimit", raw: "-5 C", present: true, want: -5, wantOK: true},
		{name: "bare integer", raw: "4000", present: true, want: 4000, wantOK: true},
		// A decimal body is not an integer reading. Staying strict keeps the
		// encoder/FBC validators able to reject a non-integer session count;
		// power readings use floatValue instead.
		{name: "watts are not an integer reading", raw: "1000.00 W", present: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.present, tc.raw.present())
			assert.Equal(t, tc.unsupported, tc.raw.unsupported())
			assert.Equal(t, tc.failed, tc.raw.failed())
			got, ok := tc.raw.intValue()
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestReading_FloatValueParsesPowerWatts(t *testing.T) {
	got, ok := reading("618.35 W").floatValue()
	require.True(t, ok)
	assert.InDelta(t, 618.35, got, 0.001)

	_, ok = reading("GPU is lost").floatValue()
	assert.False(t, ok)
}

func TestParse_DecodesCapturedAmpereDocument(t *testing.T) {
	doc, err := parse(loadFixture(t, "qx-a100-healthy.xml"))
	require.NoError(t, err)

	require.Len(t, doc.GPUs, 2)
	attached, ok := doc.AttachedGPUs.intValue()
	require.True(t, ok)
	assert.Equal(t, 2, attached)

	gpu := doc.GPUs[0]
	assert.Equal(t, "NVIDIA A100-SXM4-40GB", string(gpu.ProductName))
	assert.Equal(t, "Ampere", string(gpu.ProductArchitecture))
	assert.Equal(t, "GPU-12345678-1234-1234-1234-123456780000", string(gpu.UUID))
	assert.Equal(t, "P0", string(gpu.PerformanceState))

	// Ampere emits absolute thresholds and an N/A T.Limit.
	assert.Equal(t, "92 C", string(gpu.Temperature.MaxThreshold))
	assert.Equal(t, "87 C", string(gpu.Temperature.SlowThreshold))
	assert.Equal(t, "83 C", string(gpu.Temperature.MaxGPUThreshold))
	assert.True(t, gpu.Temperature.TLimit.unsupported())
	assert.False(t, gpu.Temperature.MaxTLimitThreshold.present())
}

func TestParse_DecodesCapturedBlackwellDocument(t *testing.T) {
	doc, err := parse(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)

	gpu := doc.GPUs[0]
	assert.Equal(t, "NVIDIA GB200", string(gpu.ProductName))

	// Blackwell replaces the absolute elements; they are absent, not N/A.
	assert.False(t, gpu.Temperature.MaxThreshold.present())
	assert.False(t, gpu.Temperature.SlowThreshold.present())
	assert.False(t, gpu.Temperature.MaxGPUThreshold.present())
	assert.Equal(t, "-5 C", string(gpu.Temperature.MaxTLimitThreshold))
	assert.Equal(t, "0 C", string(gpu.Temperature.SlowTLimitThreshold))
	assert.Equal(t, "5 C", string(gpu.Temperature.MaxGPUTLimitThreshold))

	assert.Equal(t, "1000.00 W", string(gpu.PowerReadings.CurrentPowerLimit))
	assert.Equal(t, "400.00 W", string(gpu.PowerReadings.MinPowerLimit))
	assert.Equal(t, "1200.00 W", string(gpu.PowerReadings.MaxPowerLimit))
}

// sm_clock appears under both <clocks> (the current clock) and <max_clocks>.
// --query-gpu=clocks.sm reads the current one, so the schema must bind to
// <clocks> and not pick up the maximum.
func TestParse_BindsCurrentClocksNotMaximums(t *testing.T) {
	doc, err := parse(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)

	assert.Equal(t, "345 MHz", string(doc.GPUs[0].Clocks.SMClock))
	assert.Equal(t, "345 MHz", string(doc.GPUs[0].Clocks.GraphicsClock))
}

// average_power_draw also appears under gpu_memory_power_readings and
// module_power_readings, where this profile reports N/A. Binding to the wrong
// parent would make a healthy GPU look unreadable.
func TestParse_BindsGPUPowerReadingsNotModulePower(t *testing.T) {
	doc, err := parse(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)

	assert.Equal(t, "565.11 W", string(doc.GPUs[0].PowerReadings.InstantPowerDraw))
	assert.Equal(t, "618.35 W", string(doc.GPUs[0].PowerReadings.AveragePowerDraw))
}

func TestParse_DecodesLostDeviceBodies(t *testing.T) {
	doc, err := parse(loadFixture(t, "qx-gb200-lost.xml"))
	require.NoError(t, err)

	require.Len(t, doc.GPUs, 2)
	assert.False(t, doc.GPUs[0].Utilization.GPU.failed(), "GPU 0 is healthy in this fixture")
	assert.True(t, doc.GPUs[1].Utilization.GPU.failed(), "GPU 1 is lost in this fixture")
}

func TestParse_RejectsDocumentWithNoGPUs(t *testing.T) {
	_, err := parse(`<?xml version="1.0" ?><nvidia_smi_log></nvidia_smi_log>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no GPUs")
}
