// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSnapshot_ReportsInventory(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-a100-healthy.xml"))
	require.NoError(t, err)

	attached, ok := snap.AttachedGPUs()
	require.True(t, ok)
	assert.Equal(t, 2, attached)
	assert.Equal(t, 2, snap.Count())
	assert.Equal(t, []string{"NVIDIA A100-SXM4-40GB", "NVIDIA A100-SXM4-40GB"}, snap.ProductNames())
	assert.Equal(t, []string{
		"GPU-12345678-1234-1234-1234-123456780000",
		"GPU-12345678-1234-1234-1234-123456780001",
	}, snap.UUIDs())
}

func TestSnapshot_GPURejectsOutOfRangeIndex(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-a100-healthy.xml"))
	require.NoError(t, err)

	_, err = snap.GPU(2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reported 2 GPUs")

	_, err = snap.GPU(-1)
	require.Error(t, err)
}

func TestSnapshot_DetectsFailedDevice(t *testing.T) {
	healthy, err := ParseSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	assert.False(t, healthy.HasFailedGPU())

	lost, err := ParseSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
	require.NoError(t, err)
	assert.True(t, lost.HasFailedGPU())

	// The fixture is scoped: GPU 0 healthy, GPU 1 lost. A per-GPU check must
	// see the difference, otherwise the scoping assertions are vacuous.
	gpu0, err := lost.GPU(0)
	require.NoError(t, err)
	assert.False(t, gpu0.Failed())

	gpu1, err := lost.GPU(1)
	require.NoError(t, err)
	assert.True(t, gpu1.Failed())
}

// "N/A" is an unsupported query, not a failure. A healthy passively-cooled
// profile reports fan_speed N/A, and treating that as failure would make every
// such profile look broken — the trap the old substring heuristic fell into.
func TestSnapshot_TreatsNotAvailableAsHealthy(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-a100-healthy.xml"))
	require.NoError(t, err)
	assert.False(t, snap.HasFailedGPU(), "a100 reports fan_speed N/A but is healthy")
}

func TestGPU_UncorrectedECCAggregate(t *testing.T) {
	healthy, err := ParseSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	gpu, err := healthy.GPU(0)
	require.NoError(t, err)
	total, ok := gpu.UncorrectedECCAggregate()
	require.True(t, ok, "dram_uncorrectable is numeric on a healthy GPU")
	assert.Equal(t, 0, total)

	injected, err := ParseSnapshot(loadFixture(t, "qx-gb200-ecc-injected.xml"))
	require.NoError(t, err)
	gpu, err = injected.GPU(0)
	require.NoError(t, err)
	total, ok = gpu.UncorrectedECCAggregate()
	require.True(t, ok)
	assert.Positive(t, total, "the fixture has injected uncorrectable errors")

	// The injection was scoped to GPU 0.
	other, err := injected.GPU(1)
	require.NoError(t, err)
	total, ok = other.UncorrectedECCAggregate()
	require.True(t, ok)
	assert.Equal(t, 0, total)
}

// A lost GPU renders the counters as "GPU is lost", which is neither zero nor a
// number. The caller must be able to tell that apart from a clean zero.
func TestGPU_UncorrectedECCAggregateOnFailedDevice(t *testing.T) {
	lost, err := ParseSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
	require.NoError(t, err)
	gpu, err := lost.GPU(1)
	require.NoError(t, err)
	_, ok := gpu.UncorrectedECCAggregate()
	assert.False(t, ok, "a failed device has no countable ECC total")
}

// SRAM counters read N/A on these profiles, so the sum must skip them rather
// than treat them as zero and rather than giving up on the whole reading.
func TestGPU_UncorrectedECCAggregateSkipsUnsupportedCounters(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-gb200-ecc-injected.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	require.True(t, gpu.element.ECCErrors.Aggregate.SRAMUncorrectableParity.unsupported(),
		"fixture precondition: SRAM parity is N/A")
	total, ok := gpu.UncorrectedECCAggregate()
	require.True(t, ok)
	dram, _ := gpu.element.ECCErrors.Aggregate.DRAMUncorrectable.intValue()
	assert.Equal(t, dram, total, "only the numeric counters contribute")
}

// The scenario-level check: injection hits one GPU, so a per-document maximum
// has to see it past the healthy siblings.
func TestSnapshot_MaxUncorrectedECCAggregate(t *testing.T) {
	healthy, err := ParseSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	total, ok := healthy.MaxUncorrectedECCAggregate()
	require.True(t, ok)
	assert.Equal(t, 0, total)

	injected, err := ParseSnapshot(loadFixture(t, "qx-gb200-ecc-injected.xml"))
	require.NoError(t, err)
	total, ok = injected.MaxUncorrectedECCAggregate()
	require.True(t, ok)
	assert.Equal(t, 8, total, "GPU 0 aggregate dram_uncorrectable")

	// A lost sibling reports no countable total; the healthy GPU still does, so
	// the document as a whole stays readable.
	lost, err := ParseSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
	require.NoError(t, err)
	total, ok = lost.MaxUncorrectedECCAggregate()
	require.True(t, ok)
	assert.Equal(t, 8, total)
}

// The scalar readings the runtime-control scenarios pin and read back. Every
// one replaces a --query-gpu CSV field, so the values are checked against the
// captured document rather than against a hand-written string.
func TestGPU_ScalarReadings(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	temp, ok := gpu.TemperatureC()
	require.True(t, ok)
	assert.Equal(t, 63, temp)

	util, ok := gpu.UtilizationGPUPercent()
	require.True(t, ok)
	assert.Equal(t, 50, util)

	memUtil, ok := gpu.UtilizationMemoryPercent()
	require.True(t, ok)
	assert.Equal(t, 25, memUtil)

	// The current clock, not the 2100 MHz max_clocks sibling that reuses the
	// element name.
	clock, ok := gpu.SMClockMHz()
	require.True(t, ok)
	assert.Equal(t, 345, clock)

	assert.Equal(t, "P0", gpu.PerformanceState())
	assert.Equal(t, "Not Active", gpu.ThermalSlowdownState())

	// The framebuffer, not the bar1_memory_usage sibling that reuses the
	// element names.
	used, ok := gpu.MemoryUsedMiB()
	require.True(t, ok)
	assert.Equal(t, 0, used)

	total, ok := gpu.MemoryTotalMiB()
	require.True(t, ok)
	assert.Equal(t, 196608, total)
}

func TestGPU_PowerReadings(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	// The <gpu_power_readings> block, not the gpu_memory_power_readings and
	// module_power_readings siblings that repeat the same element names as N/A.
	limit, ok := gpu.PowerLimitW()
	require.True(t, ok)
	assert.InDelta(t, 1000.0, limit, 0.01)

	minW, ok := gpu.PowerMinLimitW()
	require.True(t, ok)
	assert.InDelta(t, 400.0, minW, 0.01)

	maxW, ok := gpu.PowerMaxLimitW()
	require.True(t, ok)
	assert.InDelta(t, 1200.0, maxW, 0.01)

	// The mock resolves instant and average from the same getter, so either
	// element answers power.draw; instant is sampled later, hence preferred.
	draw, ok := gpu.PowerDrawW()
	require.True(t, ok)
	assert.InDelta(t, 565.11, draw, 0.01)
}

// A lost GPU renders every power element as an error body, so no reading is
// numeric and the caller must be told rather than handed a zero.
func TestGPU_PowerReadingsOnFailedDevice(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(1)
	require.NoError(t, err)

	_, ok := gpu.PowerDrawW()
	assert.False(t, ok)
	_, ok = gpu.TemperatureC()
	assert.False(t, ok)
	_, ok = gpu.SMClockMHz()
	assert.False(t, ok)
}

// fan_speed is the reading that made the old "does the output contain N/A"
// heuristic wrong: these profiles are passively cooled, so N/A is the healthy
// baseline and has to round-trip as itself.
func TestGPU_FanSpeed(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	assert.Equal(t, "N/A", gpu.FanSpeed())
	_, ok := gpu.FanSpeedPercent()
	assert.False(t, ok, "N/A is not a percentage")

	pinned, err := ParseSnapshot(strings.Replace(loadFixture(t, "qx-gb200-healthy.xml"),
		"<fan_speed>N/A</fan_speed>", "<fan_speed>57 %</fan_speed>", 1))
	require.NoError(t, err)
	gpu, err = pinned.GPU(0)
	require.NoError(t, err)
	assert.Equal(t, "57 %", gpu.FanSpeed())
	pct, ok := gpu.FanSpeedPercent()
	require.True(t, ok)
	assert.Equal(t, 57, pct)
}

func TestGPU_ProcessesDecodesConfiguredEntries(t *testing.T) {
	out := strings.Replace(loadFixture(t, "qx-gb200-healthy.xml"),
		"<processes>\n\t\t</processes>",
		"<processes>\n\t\t\t<process_info><pid>4201</pid><process_name>train.py</process_name>"+
			"<used_memory>1024 MiB</used_memory></process_info>\n\t\t</processes>", 1)

	snap, err := ParseSnapshot(out)
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	got, err := gpu.Processes()
	require.NoError(t, err)
	assert.Equal(t, []Process{{PID: 4201, Name: "train.py", MemoryMiB: 1024}}, got)

	// The second GPU is untouched, which is how the scoping assertions read.
	other, err := snap.GPU(1)
	require.NoError(t, err)
	empty, err := other.Processes()
	require.NoError(t, err)
	assert.Empty(t, empty)
}
