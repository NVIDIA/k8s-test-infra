// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventoryProblems_AcceptsMatchingProfile(t *testing.T) {
	problems := InventoryProblems(loadFixture(t, "qx-a100-healthy.xml"), "NVIDIA A100-SXM4-40GB", 2)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestInventoryProblems_RejectsWrongCount(t *testing.T) {
	problems := InventoryProblems(loadFixture(t, "qx-a100-healthy.xml"), "NVIDIA A100-SXM4-40GB", 8)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "want 8")
}

// The check is equality, not substring: product_name carries the profile's
// DisplayName verbatim, so a truncated or decorated name must fail.
func TestInventoryProblems_RejectsWrongProductName(t *testing.T) {
	problems := InventoryProblems(loadFixture(t, "qx-a100-healthy.xml"), "NVIDIA A100", 2)
	require.Len(t, problems, 2, "both GPUs carry the wrong name")
	assert.Contains(t, problems[0], "product_name")
}

// attached_gpus and the number of <gpu> elements must agree; a mismatch means
// nvidia-smi truncated the document and every later index is suspect.
func TestInventoryProblems_RejectsTruncatedDocument(t *testing.T) {
	out := strings.Replace(loadFixture(t, "qx-a100-healthy.xml"),
		"<attached_gpus>2</attached_gpus>", "<attached_gpus>4</attached_gpus>", 1)
	problems := InventoryProblems(out, "NVIDIA A100-SXM4-40GB", 4)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "2 <gpu> elements")
}

func TestInventoryProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := InventoryProblems("not xml", "NVIDIA A100-SXM4-40GB", 2)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

func TestPhantomProcessProblems_AcceptsIdleGPUs(t *testing.T) {
	problems := PhantomProcessProblems(loadFixture(t, "qx-gb200-healthy.xml"))
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The phantom-process regression: a stub that returned SUCCESS without zeroing
// the caller's count made nvidia-smi render its uninitialised buffer as
// hundreds of PID 0 entries.
func TestPhantomProcessProblems_RejectsPhantomProcesses(t *testing.T) {
	out := strings.Replace(loadFixture(t, "qx-gb200-healthy.xml"),
		"<processes>\n\t\t</processes>",
		"<processes>\n\t\t\t<process_info><pid>0</pid><process_name>N/A</process_name>"+
			"<used_memory>0 MiB</used_memory></process_info>\n\t\t</processes>", 1)
	problems := PhantomProcessProblems(out)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "pid 0")
}

// The JPEG/OFA and encoder/FBC checks below are driven by hand-built documents
// rather than the captured fixtures: the readings under test are N/A in every
// capture taken before the NVML getters existed, so the passing case has to be
// constructed.

// xmlDocument wraps <gpu> elements in the envelope nvidia-smi emits.
func xmlDocument(gpus ...string) string {
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

func TestJpgOfaUtilizationProblems_AcceptsConfiguredPercentages(t *testing.T) {
	out := xmlDocument(
		utilizationGPU("0000:07:00.0", jpeg35, ofa12),
		utilizationGPU("0000:0F:00.0", jpeg35, ofa12),
	)
	require.Empty(t, JpgOfaUtilizationProblems(out, 35, 12))
}

func TestJpgOfaUtilizationProblems_RejectsNotAvailableReadings(t *testing.T) {
	out := xmlDocument(utilizationGPU("0000:07:00.0", jpegNA, ofaNA))

	problems := JpgOfaUtilizationProblems(out, 35, 12)
	require.Len(t, problems, 2, "both readings are N/A")
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, `jpeg_util = "N/A"`)
	assert.Contains(t, joined, `ofa_util = "N/A"`)
}

// A zeroed default reading must not satisfy a non-zero expectation, and the two
// values must not be interchangeable — the reason the fixture uses 35 and 12.
func TestJpgOfaUtilizationProblems_RejectsWrongPercentages(t *testing.T) {
	out := xmlDocument(utilizationGPU("0000:07:00.0", jpeg35, ofa12))

	assert.Len(t, JpgOfaUtilizationProblems(out, 0, 0), 2,
		"35 %% / 12 %% must not satisfy a zeroed expectation")
	assert.Len(t, JpgOfaUtilizationProblems(out, 12, 35), 2,
		"transposed expectations must not pass")
}

// Absent elements decode as empty strings, which must be reported rather than
// read as 0 %.
func TestJpgOfaUtilizationProblems_ReportsMissingElements(t *testing.T) {
	out := xmlDocument(utilizationGPU("0000:07:00.0", "", ""))

	problems := JpgOfaUtilizationProblems(out, 35, 12)
	require.Len(t, problems, 2)
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, `jpeg_util = ""`)
	assert.Contains(t, joined, `ofa_util = ""`)
}

// A getter that answers for only the first device must fail, and the report must
// name the GPU so the failure points at a device.
func TestJpgOfaUtilizationProblems_ChecksEveryGPU(t *testing.T) {
	out := xmlDocument(
		utilizationGPU("0000:07:00.0", jpeg35, ofa12),
		utilizationGPU("0000:0F:00.0", jpegNA, ofa12),
	)

	problems := JpgOfaUtilizationProblems(out, 35, 12)
	require.Len(t, problems, 1, "only the second GPU's jpeg_util is N/A")
	assert.Contains(t, problems[0], "0000:0F:00.0")
}

// nvidia-smi can die mid-document and still exit with a partial tree on stdout;
// truncated XML must be reported as a parse failure, not silently pass for want
// of any <gpu> elements to check.
func TestJpgOfaUtilizationProblems_RejectsTruncatedOutput(t *testing.T) {
	full := xmlDocument(utilizationGPU("0000:07:00.0", jpeg35, ofa12))
	truncated := full[:strings.Index(full, "<utilization>")]

	problems := JpgOfaUtilizationProblems(truncated, 35, 12)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

func TestJpgOfaUtilizationProblems_RejectsOutputWithoutGPUs(t *testing.T) {
	problems := JpgOfaUtilizationProblems(xmlDocument(), 35, 12)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "no GPUs")
}

// statsGPU renders one <gpu> whose encoder_stats, fbc_stats and
// accounting_mode_buffer_size bodies are the given string, so a test can pass
// either configured numbers or the N/A a missing NVML getter renders.
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

// pcieIdentityGPU renders one <gpu> whose three PCIe maxima agree, the healthy
// shape: a host and a device of the same generation, and a link negotiated to it.
func pcieIdentityGPU(id, boardID, gen string) string {
	return pcieIdentityGPUGens(id, boardID, gen, gen, gen)
}

// pcieIdentityGPUGens renders one <gpu> with each maximum set independently.
// Every body is injected verbatim so a test can supply "4", "N/A" or nothing.
// The captured fixtures cannot drive the passing case here: they predate the
// host-max fix and carry max_host_link_gen 0.
func pcieIdentityGPUGens(id, boardID, maxGen, deviceMaxGen, hostMaxGen string) string {
	return `
	<gpu id="` + id + `">
		<product_name>NVIDIA A100-SXM4-40GB</product_name>
		<board_id>` + boardID + `</board_id>
		<pci>
			<pci_bus_id>` + id + `</pci_bus_id>
			<pci_gpu_link_info>
				<pcie_gen>
					<max_link_gen>` + maxGen + `</max_link_gen>
					<current_link_gen>` + maxGen + `</current_link_gen>
					<device_current_link_gen>` + maxGen + `</device_current_link_gen>
					<max_device_link_gen>` + deviceMaxGen + `</max_device_link_gen>
					<max_host_link_gen>` + hostMaxGen + `</max_host_link_gen>
				</pcie_gen>
				<link_widths>
					<max_link_width>16x</max_link_width>
					<current_link_width>16x</current_link_width>
				</link_widths>
			</pci_gpu_link_info>
		</pci>
	</gpu>`
}

func TestPCIeIdentityProblems_AcceptsFixedOutput(t *testing.T) {
	out := xmlDocument(
		pcieIdentityGPU("0000:07:00.0", "0x700", "4"),
		pcieIdentityGPU("0000:0F:00.0", "0xf00", "4"),
	)
	problems := PCIeIdentityProblems(out, 2, 4)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// Gen6 pins the expectation to the profile's configured generation rather than a
// constant: one check must accept 6 on Blackwell and 4 on a100.
func TestPCIeIdentityProblems_AcceptsGen6Output(t *testing.T) {
	out := xmlDocument(
		pcieIdentityGPU("0000:0A:00.0", "0xa00", "6"),
		pcieIdentityGPU("0000:0B:00.0", "0xb00", "6"),
	)
	problems := PCIeIdentityProblems(out, 2, 6)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The defect as captured before the fix: board_id 0x0 on every GPU,
// max_device_link_gen N/A from the generated stub, and a Gen0 host maximum.
func TestPCIeIdentityProblems_RejectsBuggyOutput(t *testing.T) {
	out := xmlDocument(
		pcieIdentityGPUGens("0000:07:00.0", "0x0", "4", "N/A", "0"),
		pcieIdentityGPUGens("0000:0F:00.0", "0x0", "4", "N/A", "0"),
	)

	problems := PCIeIdentityProblems(out, 2, 4)
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "max_device_link_gen")
	assert.Contains(t, joined, "max_host_link_gen")
	assert.Contains(t, joined, "board_id")
}

// The Host Max half of #638 in isolation: nvidia-smi rendered Gen0 for the host
// side of a link both the GPU and the negotiated maximum put at Gen4.
func TestPCIeIdentityProblems_RejectsZeroHostMax(t *testing.T) {
	out := xmlDocument(pcieIdentityGPUGens("0000:07:00.0", "0x700", "4", "4", "0"))

	problems := PCIeIdentityProblems(out, 1, 4)
	require.Len(t, problems, 1, "only the host maximum is wrong: %s", strings.Join(problems, "; "))
	assert.Contains(t, problems[0], "max_host_link_gen = 0, want 4")
}

// A host maximum below the device maximum is equally impossible: the link could
// not have negotiated Gen4 through a Gen3 host.
func TestPCIeIdentityProblems_RejectsHostMaxBelowDeviceMax(t *testing.T) {
	out := xmlDocument(pcieIdentityGPUGens("0000:07:00.0", "0x700", "4", "4", "3"))

	problems := PCIeIdentityProblems(out, 1, 4)
	require.Len(t, problems, 1, strings.Join(problems, "; "))
	assert.Contains(t, problems[0], "max_host_link_gen = 3, want 4")
}

func TestPCIeIdentityProblems_RejectsDuplicateBoardIDs(t *testing.T) {
	out := xmlDocument(
		pcieIdentityGPU("0000:07:00.0", "0x700", "4"),
		pcieIdentityGPU("0000:0F:00.0", "0x700", "4"),
	)

	problems := PCIeIdentityProblems(out, 2, 4)
	require.NotEmpty(t, problems, "distinct GPUs sharing a board ID must be reported")
	assert.Contains(t, strings.Join(problems, "; "), "duplicates")
}

// Duplicate detection keys on the parsed value, so a leading zero cannot hide a
// collision.
func TestPCIeIdentityProblems_RejectsEquivalentBoardIDRenderings(t *testing.T) {
	out := xmlDocument(
		pcieIdentityGPU("0000:07:00.0", "0x700", "4"),
		pcieIdentityGPU("0000:0F:00.0", "0x0700", "4"),
	)

	problems := PCIeIdentityProblems(out, 2, 4)
	require.NotEmpty(t, problems, "0x0700 is the same board as 0x700")
	assert.Contains(t, strings.Join(problems, "; "), "duplicates")
}

// Absent elements decode as empty strings, which must be reported rather than
// read as board 0 or Gen0.
func TestPCIeIdentityProblems_ReportsMissingReadings(t *testing.T) {
	out := xmlDocument(pcieIdentityGPUGens("0000:07:00.0", "", "4", "", ""))

	problems := PCIeIdentityProblems(out, 1, 4)
	require.Len(t, problems, 3, "the board ID and both endpoint maxima are absent")
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "board_id")
	assert.Contains(t, joined, "max_device_link_gen")
	assert.Contains(t, joined, "max_host_link_gen")
}

func TestPCIeIdentityProblems_RejectsWrongGPUCount(t *testing.T) {
	out := xmlDocument(pcieIdentityGPU("0000:07:00.0", "0x700", "4"))

	problems := PCIeIdentityProblems(out, 8, 4)
	require.NotEmpty(t, problems, "a truncated GPU list must be reported")
	assert.Contains(t, strings.Join(problems, "; "), "reported 1 GPUs, want 8")
}

func TestPCIeIdentityProblems_RejectsUnparseableOutput(t *testing.T) {
	require.NotEmpty(t, PCIeIdentityProblems("not xml at all", 2, 4))
	require.NotEmpty(t, PCIeIdentityProblems(xmlDocument(), 0, 4), "a GPU-less document is an error")
}

// A real nvidia-smi document, so the element nesting is verified against the
// driver rather than against the hand-built XML above: the a100 capture carries
// the derived board IDs (0x700 and 0xf00) and a Gen4 link. It was taken before
// the host-max fix, so its max_host_link_gen is the one reading still expected
// to be reported — and nothing else, which is what pins the other paths.
func TestPCIeIdentityProblems_ReadsCapturedDocument(t *testing.T) {
	problems := PCIeIdentityProblems(loadFixture(t, "qx-a100-healthy.xml"), 2, 4)

	for _, p := range problems {
		assert.Contains(t, p, "max_host_link_gen",
			"the capture predates the host-max fix; every other PCIe reading must already be right")
	}
}

func TestEncoderFBCProblems_AcceptsConfiguredStats(t *testing.T) {
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}

	problems := EncoderFBCProblems(xmlDocument(statsGPU("2", "30", "1500 us", "4000")), want, want, 4000)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The stubbed exports rendered every one of these as N/A, which must be
// reported as a missing getter rather than compared as a number.
func TestEncoderFBCProblems_RejectsStubValues(t *testing.T) {
	want := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}

	problems := EncoderFBCProblems(xmlDocument(statsGPU("N/A", "N/A", "N/A", "N/A")), want, want, 4000)
	require.Len(t, problems, 7, "three readings per stats block plus the accounting buffer")
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "encoder_stats session_count")
	assert.Contains(t, joined, "fbc_stats average_latency")
	assert.Contains(t, joined, "accounting_mode_buffer_size")
	assert.Contains(t, joined, "getter is missing or unimplemented")
}

// Encoder and FBC carry separate expectations, so a getter that serves one
// block's numbers for both must be caught.
func TestEncoderFBCProblems_ChecksBothBlocksAgainstTheirOwnExpectation(t *testing.T) {
	encoder := EncoderFBCStats{SessionCount: 2, AverageFPS: 30, AverageLatencyUS: 1500}
	fbc := EncoderFBCStats{SessionCount: 4, AverageFPS: 60, AverageLatencyUS: 900}

	problems := EncoderFBCProblems(xmlDocument(statsGPU("2", "30", "1500 us", "4000")), encoder, fbc, 4000)
	require.Len(t, problems, 3, "the fbc_stats block matches the encoder expectation")
	assert.Contains(t, strings.Join(problems, "; "), "fbc_stats session_count = 2, want 4")
}
