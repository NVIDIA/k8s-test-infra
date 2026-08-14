// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGPUSnapshot_ReportsInventory(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-a100-healthy.xml"))
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

func TestGPUSnapshot_GPURejectsOutOfRangeIndex(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-a100-healthy.xml"))
	require.NoError(t, err)

	_, err = snap.GPU(2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reported 2 GPUs")

	_, err = snap.GPU(-1)
	require.Error(t, err)
}

func TestDiffInventoryXML_AcceptsMatchingProfile(t *testing.T) {
	problems := DiffInventoryXML(loadFixture(t, "qx-a100-healthy.xml"), "NVIDIA A100-SXM4-40GB", 2)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestDiffInventoryXML_RejectsWrongCount(t *testing.T) {
	problems := DiffInventoryXML(loadFixture(t, "qx-a100-healthy.xml"), "NVIDIA A100-SXM4-40GB", 8)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "want 8")
}

// The check is equality, not substring: product_name carries the profile's
// DisplayName verbatim, so a truncated or decorated name must fail.
func TestDiffInventoryXML_RejectsWrongProductName(t *testing.T) {
	problems := DiffInventoryXML(loadFixture(t, "qx-a100-healthy.xml"), "NVIDIA A100", 2)
	require.Len(t, problems, 2, "both GPUs carry the wrong name")
	assert.Contains(t, problems[0], "product_name")
}

// attached_gpus and the number of <gpu> elements must agree; a mismatch means
// nvidia-smi truncated the document and every later index is suspect.
func TestDiffInventoryXML_RejectsTruncatedDocument(t *testing.T) {
	out := strings.Replace(loadFixture(t, "qx-a100-healthy.xml"),
		"<attached_gpus>2</attached_gpus>", "<attached_gpus>4</attached_gpus>", 1)
	problems := DiffInventoryXML(out, "NVIDIA A100-SXM4-40GB", 4)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "2 <gpu> elements")
}

func TestDiffInventoryXML_ReportsUnparseableDocument(t *testing.T) {
	problems := DiffInventoryXML("not xml", "NVIDIA A100-SXM4-40GB", 2)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

func TestDiffNoProcessesXML_AcceptsIdleGPUs(t *testing.T) {
	problems := DiffNoProcessesXML(loadFixture(t, "qx-gb200-healthy.xml"))
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The phantom-process regression: a stub that returned SUCCESS without zeroing
// the caller's count made nvidia-smi render its uninitialised buffer as
// hundreds of PID 0 entries.
func TestDiffNoProcessesXML_RejectsPhantomProcesses(t *testing.T) {
	out := strings.Replace(loadFixture(t, "qx-gb200-healthy.xml"),
		"<processes>\n\t\t</processes>",
		"<processes>\n\t\t\t<process_info><pid>0</pid><process_name>N/A</process_name>"+
			"<used_memory>0 MiB</used_memory></process_info>\n\t\t</processes>", 1)
	problems := DiffNoProcessesXML(out)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "pid 0")
}

func TestGPUSnapshot_DetectsFailedDevice(t *testing.T) {
	healthy, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	assert.False(t, healthy.HasFailedGPU())

	lost, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
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
func TestGPUSnapshot_TreatsNotAvailableAsHealthy(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-a100-healthy.xml"))
	require.NoError(t, err)
	assert.False(t, snap.HasFailedGPU(), "a100 reports fan_speed N/A but is healthy")
}

func TestGPUReadings_UncorrectedECCAggregate(t *testing.T) {
	healthy, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	gpu, err := healthy.GPU(0)
	require.NoError(t, err)
	total, ok := gpu.UncorrectedECCAggregate()
	require.True(t, ok, "dram_uncorrectable is numeric on a healthy GPU")
	assert.Equal(t, 0, total)

	injected, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-ecc-injected.xml"))
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
func TestGPUReadings_UncorrectedECCAggregateOnFailedDevice(t *testing.T) {
	lost, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
	require.NoError(t, err)
	gpu, err := lost.GPU(1)
	require.NoError(t, err)
	_, ok := gpu.UncorrectedECCAggregate()
	assert.False(t, ok, "a failed device has no countable ECC total")
}

// SRAM counters read N/A on these profiles, so the sum must skip them rather
// than treat them as zero and rather than giving up on the whole reading.
func TestGPUReadings_UncorrectedECCAggregateSkipsUnsupportedCounters(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-ecc-injected.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	require.True(t, gpu.gpu.ECCErrors.Aggregate.SRAMUncorrectableParity.unsupported(),
		"fixture precondition: SRAM parity is N/A")
	total, ok := gpu.UncorrectedECCAggregate()
	require.True(t, ok)
	dram, _ := gpu.gpu.ECCErrors.Aggregate.DRAMUncorrectable.intValue()
	assert.Equal(t, dram, total, "only the numeric counters contribute")
}

// The scenario-level check: injection hits one GPU, so a per-document maximum
// has to see it past the healthy siblings.
func TestGPUSnapshot_MaxUncorrectedECCAggregate(t *testing.T) {
	healthy, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	total, ok := healthy.MaxUncorrectedECCAggregate()
	require.True(t, ok)
	assert.Equal(t, 0, total)

	injected, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-ecc-injected.xml"))
	require.NoError(t, err)
	total, ok = injected.MaxUncorrectedECCAggregate()
	require.True(t, ok)
	assert.Equal(t, 8, total, "GPU 0 aggregate dram_uncorrectable")

	// A lost sibling reports no countable total; the healthy GPU still does, so
	// the document as a whole stays readable.
	lost, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
	require.NoError(t, err)
	total, ok = lost.MaxUncorrectedECCAggregate()
	require.True(t, ok)
	assert.Equal(t, 8, total)
}

// The scalar readings the runtime-control scenarios pin and read back. Every
// one replaces a --query-gpu CSV field, so the values are checked against the
// captured document rather than against a hand-written string.
func TestGPUReadings_ScalarReadings(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
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

func TestGPUReadings_PowerReadings(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
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
func TestGPUReadings_PowerReadingsOnFailedDevice(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-lost.xml"))
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
func TestGPUReadings_FanSpeed(t *testing.T) {
	snap, err := ParseGPUSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	assert.Equal(t, "N/A", gpu.FanSpeed())
	_, ok := gpu.FanSpeedPercent()
	assert.False(t, ok, "N/A is not a percentage")

	pinned, err := ParseGPUSnapshot(strings.Replace(loadFixture(t, "qx-gb200-healthy.xml"),
		"<fan_speed>N/A</fan_speed>", "<fan_speed>57 %</fan_speed>", 1))
	require.NoError(t, err)
	gpu, err = pinned.GPU(0)
	require.NoError(t, err)
	assert.Equal(t, "57 %", gpu.FanSpeed())
	pct, ok := gpu.FanSpeedPercent()
	require.True(t, ok)
	assert.Equal(t, 57, pct)
}

func TestGPUReadings_ProcessesDecodesConfiguredEntries(t *testing.T) {
	out := strings.Replace(loadFixture(t, "qx-gb200-healthy.xml"),
		"<processes>\n\t\t</processes>",
		"<processes>\n\t\t\t<process_info><pid>4201</pid><process_name>train.py</process_name>"+
			"<used_memory>1024 MiB</used_memory></process_info>\n\t\t</processes>", 1)

	snap, err := ParseGPUSnapshot(out)
	require.NoError(t, err)
	gpu, err := snap.GPU(0)
	require.NoError(t, err)

	got, err := gpu.Processes()
	require.NoError(t, err)
	assert.Equal(t, []SMIProcess{{PID: 4201, Name: "train.py", MemoryMiB: 1024}}, got)

	// The second GPU is untouched, which is how the scoping assertions read.
	other, err := snap.GPU(1)
	require.NoError(t, err)
	empty, err := other.Processes()
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// The defect checks below are driven by hand-built documents rather than the
// captured fixtures: the readings under test are N/A in every capture taken
// before the NVML getters existed, so the passing case has to be constructed.

// smiXML wraps <gpu> elements in the envelope nvidia-smi emits.
func smiXML(gpus ...string) string {
	return `<?xml version="1.0" ?>
<!DOCTYPE nvidia_smi_log SYSTEM "nvsmi_device_v13.dtd">
<nvidia_smi_log>
	<driver_version>550.163.01</driver_version>
	<attached_gpus>` + strconv.Itoa(len(gpus)) + `</attached_gpus>` +
		strings.Join(gpus, "") + `
</nvidia_smi_log>`
}

// utilizationGPU renders one <gpu> element whose utilization block carries the
// given jpeg_util and ofa_util bodies verbatim, so a test can inject "35 %",
// "N/A" or nothing at all.
func utilizationGPU(id, jpeg, ofa string) string {
	return `
	<gpu id="` + id + `">
		<product_name>NVIDIA A100-SXM4-40GB</product_name>
		<utilization>
			<gpu_util>40 %</gpu_util>
			<memory_util>19 %</memory_util>
			<encoder_util>0 %</encoder_util>
			<decoder_util>0 %</decoder_util>` + jpeg + ofa + `
		</utilization>
	</gpu>`
}

const (
	jpeg35 = "\n\t\t\t<jpeg_util>35 %</jpeg_util>"
	ofa12  = "\n\t\t\t<ofa_util>12 %</ofa_util>"
	jpegNA = "\n\t\t\t<jpeg_util>N/A</jpeg_util>"
	ofaNA  = "\n\t\t\t<ofa_util>N/A</ofa_util>"
)

func TestDiffJpgOfaUtilizationXML_AcceptsConfiguredPercentages(t *testing.T) {
	out := smiXML(
		utilizationGPU("0000:07:00.0", jpeg35, ofa12),
		utilizationGPU("0000:0F:00.0", jpeg35, ofa12),
	)
	require.Empty(t, DiffJpgOfaUtilizationXML(out, 35, 12))
}

func TestDiffJpgOfaUtilizationXML_RejectsNotAvailableReadings(t *testing.T) {
	out := smiXML(utilizationGPU("0000:07:00.0", jpegNA, ofaNA))

	problems := DiffJpgOfaUtilizationXML(out, 35, 12)
	require.Len(t, problems, 2, "both readings are N/A")
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, `jpeg_util = "N/A"`)
	assert.Contains(t, joined, `ofa_util = "N/A"`)
}

// A zeroed default reading must not satisfy a non-zero expectation, and the two
// values must not be interchangeable — the reason the fixture uses 35 and 12.
func TestDiffJpgOfaUtilizationXML_RejectsWrongPercentages(t *testing.T) {
	out := smiXML(utilizationGPU("0000:07:00.0", jpeg35, ofa12))

	assert.Len(t, DiffJpgOfaUtilizationXML(out, 0, 0), 2,
		"35 %% / 12 %% must not satisfy a zeroed expectation")
	assert.Len(t, DiffJpgOfaUtilizationXML(out, 12, 35), 2,
		"transposed expectations must not pass")
}

// Absent elements decode as empty strings, which must be reported rather than
// read as 0 %.
func TestDiffJpgOfaUtilizationXML_ReportsMissingElements(t *testing.T) {
	out := smiXML(utilizationGPU("0000:07:00.0", "", ""))

	problems := DiffJpgOfaUtilizationXML(out, 35, 12)
	require.Len(t, problems, 2)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, `jpeg_util = ""`)
	assert.Contains(t, joined, `ofa_util = ""`)
}

// A getter that answers for only the first device must fail, and the report must
// name the GPU so the failure points at a device.
func TestDiffJpgOfaUtilizationXML_ChecksEveryGPU(t *testing.T) {
	out := smiXML(
		utilizationGPU("0000:07:00.0", jpeg35, ofa12),
		utilizationGPU("0000:0F:00.0", jpegNA, ofa12),
	)

	problems := DiffJpgOfaUtilizationXML(out, 35, 12)
	require.Len(t, problems, 1, "only the second GPU's jpeg_util is N/A")
	assert.Contains(t, problems[0], "0000:0F:00.0")
}

// nvidia-smi can die mid-document and still exit with a partial tree on stdout;
// truncated XML must be reported as a parse failure, not silently pass for want
// of any <gpu> elements to check.
func TestDiffJpgOfaUtilizationXML_RejectsTruncatedOutput(t *testing.T) {
	full := smiXML(utilizationGPU("0000:07:00.0", jpeg35, ofa12))
	truncated := full[:strings.Index(full, "<utilization>")]

	problems := DiffJpgOfaUtilizationXML(truncated, 35, 12)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

func TestDiffJpgOfaUtilizationXML_RejectsOutputWithoutGPUs(t *testing.T) {
	problems := DiffJpgOfaUtilizationXML(smiXML(), 35, 12)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "no GPUs")
}

// statsGPU renders one <gpu> whose encoder_stats, fbc_stats and
// accounting_mode_buffer_size bodies are the given string, so a test can pass
// the configured numbers or the N/A the stubs used to return.
func statsGPU(sessionCount, fps, latency, buffer string) string {
	return `
	<gpu id="0000:07:00.0">
		<accounting_mode_buffer_size>` + buffer + `</accounting_mode_buffer_size>
		<encoder_stats>
			<session_count>` + sessionCount + `</session_count>
			<average_fps>` + fps + `</average_fps>
			<average_latency>` + latency + `</average_latency>
		</encoder_stats>
		<fbc_stats>
			<session_count>` + sessionCount + `</session_count>
			<average_fps>` + fps + `</average_fps>
			<average_latency>` + latency + `</average_latency>
		</fbc_stats>
	</gpu>`
}

func TestDiffEncoderFBCXML_AcceptsConfiguredStats(t *testing.T) {
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}

	problems := DiffEncoderFBCXML(smiXML(statsGPU("2", "30", "1500 us", "4000")), want, want, 4000)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The stubbed exports rendered every one of these as N/A, which must be
// reported as a missing getter rather than compared as a number.
func TestDiffEncoderFBCXML_RejectsStubValues(t *testing.T) {
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}

	problems := DiffEncoderFBCXML(smiXML(statsGPU("N/A", "N/A", "N/A", "N/A")), want, want, 4000)
	require.Len(t, problems, 7, "three readings per stats block plus the accounting buffer")
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "encoder_stats session_count")
	assert.Contains(t, joined, "fbc_stats average_latency")
	assert.Contains(t, joined, "accounting_mode_buffer_size")
	assert.Contains(t, joined, "getter is missing or unimplemented")
}

// Encoder and FBC carry separate expectations, so a getter that serves one
// block's numbers for both must be caught.
func TestDiffEncoderFBCXML_ChecksBothBlocksAgainstTheirOwnExpectation(t *testing.T) {
	encoder := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}
	fbc := EncoderFBCStats{SessionCount: 4, AverageFPS: 60, AverageLatencyUS: 900}

	problems := DiffEncoderFBCXML(smiXML(statsGPU("2", "30", "1500 us", "4000")), encoder, fbc, 4000)
	require.Len(t, problems, 3, "the fbc_stats block matches the encoder expectation")
	assert.Contains(t, strings.Join(problems, "; "), "fbc_stats session_count = 2, want 4")
}
