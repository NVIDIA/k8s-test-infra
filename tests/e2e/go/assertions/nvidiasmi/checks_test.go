// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"regexp"
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

// c2cGPU renders one <gpu> carrying the given c2c_mode body — or no such
// element at all when it is empty — alongside a gpu_temp body, which is what
// GPU.Failed reads to tell a lost device from a healthy one.
func c2cGPU(id, c2cMode, gpuTemp string) string {
	element := ""
	if c2cMode != "" {
		element = "\n\t\t<c2c_mode>" + c2cMode + "</c2c_mode>"
	}
	return `
	<gpu id="` + id + `">
		<product_name>NVIDIA GB200</product_name>` + element + `
		<temperature>
			<gpu_temp>` + gpuTemp + `</gpu_temp>
		</temperature>
	</gpu>`
}

func TestC2CModeProblems_AcceptsEnabledOnGraceProfile(t *testing.T) {
	problems := C2CModeProblems(loadFixture(t, "qx-gb200-healthy.xml"), true)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestC2CModeProblems_AcceptsNotAvailableOnNonGraceProfile(t *testing.T) {
	problems := C2CModeProblems(loadFixture(t, "qx-a100-healthy.xml"), false)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The #639 regression itself: the stubbed entry point made every board read N/A,
// including the Grace ones whose defining feature is the C2C link.
func TestC2CModeProblems_RejectsNotAvailableWhenEnabledExpected(t *testing.T) {
	problems := C2CModeProblems(loadFixture(t, "qx-a100-healthy.xml"), true)
	require.Len(t, problems, 2, "both GPUs read N/A")
	assert.Contains(t, strings.Join(problems, "; "), `c2c_mode = "N/A", want "Enabled"`)
}

// The other direction, which keeps the fix from being "always report Enabled":
// a non-Grace board must not satisfy the check.
func TestC2CModeProblems_RejectsEnabledWhenNotAvailableExpected(t *testing.T) {
	problems := C2CModeProblems(loadFixture(t, "qx-gb200-healthy.xml"), false)
	require.Len(t, problems, 2, "both GPUs read Enabled")
	assert.Contains(t, strings.Join(problems, "; "), `c2c_mode = "Enabled", want "N/A"`)
}

// "Disabled" is a third state the engine deliberately never reports: a board
// without the link answers NOT_SUPPORTED, which renders as N/A. It must
// therefore satisfy neither expectation.
func TestC2CModeProblems_RejectsDisabledBody(t *testing.T) {
	out := xmlDocument(c2cGPU("0000:07:00.0", "Disabled", "36 C"))

	assert.Len(t, C2CModeProblems(out, false), 1, `"Disabled" must not pass for N/A`)
	assert.Len(t, C2CModeProblems(out, true), 1, `"Disabled" must not pass for Enabled`)
}

// An absent element is reported as such rather than compared as an empty body,
// because that is what a driver renaming the element looks like.
func TestC2CModeProblems_ReportsMissingElement(t *testing.T) {
	problems := C2CModeProblems(xmlDocument(c2cGPU("0000:07:00.0", "", "36 C")), true)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "emits no c2c_mode element")
}

// A lost GPU's reading is not evidence either way: c2c_mode is answered from a
// handle-lookup path that does not tick the failure injector, so whether the
// board's real state or an NVML error body appears depends on which element
// nvidia-smi asked for first. The healthy siblings are still checked.
func TestC2CModeProblems_SkipsFailedGPU(t *testing.T) {
	out := xmlDocument(
		c2cGPU("0000:07:00.0", "Enabled", "36 C"),
		c2cGPU("0000:0F:00.0", "GPU is lost", "GPU is lost"),
	)
	assert.Empty(t, C2CModeProblems(out, true))

	wrongHealthy := xmlDocument(
		c2cGPU("0000:07:00.0", "N/A", "36 C"),
		c2cGPU("0000:0F:00.0", "GPU is lost", "GPU is lost"),
	)
	problems := C2CModeProblems(wrongHealthy, true)
	require.Len(t, problems, 1, "the healthy GPU is still compared")
	assert.Contains(t, problems[0], "0000:07:00.0")
}

// The captured lost document must pass as-is: GPU 0 is healthy Grace silicon and
// GPU 1 is skipped, so the failure-injection scenario does not need a different
// expectation from the healthy one.
func TestC2CModeProblems_AcceptsCapturedLostDocument(t *testing.T) {
	problems := C2CModeProblems(loadFixture(t, "qx-gb200-lost.xml"), true)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// Skipping failed GPUs must not let a document where every device failed pass
// for want of anything to compare.
func TestC2CModeProblems_RejectsDocumentWhereEveryGPUFailed(t *testing.T) {
	out := xmlDocument(
		c2cGPU("0000:07:00.0", "GPU is lost", "GPU is lost"),
		c2cGPU("0000:0F:00.0", "GPU is lost", "GPU is lost"),
	)
	problems := C2CModeProblems(out, true)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "every device in the document failed")
}

func TestC2CModeProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := C2CModeProblems("not xml", true)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
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

// virtualizationGPU renders one <gpu> whose gpu_virtualization_mode block
// carries the given bodies verbatim, so a test can inject the "None" of bare
// metal, the "N/A" of a missing getter, or a vGPU mode.
func virtualizationGPU(id, mode, hostVGPU, heterogeneous string) string {
	return `
	<gpu id="` + id + `">
		<product_name>NVIDIA A100-SXM4-40GB</product_name>
		<gpu_virtualization_mode>
			<virtualization_mode>` + mode + `</virtualization_mode>
			<host_vgpu_mode>` + hostVGPU + `</host_vgpu_mode>
			<vgpu_heterogeneous_mode>` + heterogeneous + `</vgpu_heterogeneous_mode>
		</gpu_virtualization_mode>
	</gpu>`
}

func TestVirtualizationModeProblems_AcceptsBareMetalDocument(t *testing.T) {
	out := xmlDocument(
		virtualizationGPU("0000:07:00.0", "None", "N/A", "N/A"),
		virtualizationGPU("0000:0F:00.0", "None", "N/A", "N/A"),
	)

	problems := VirtualizationModeProblems(out)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The #640 defect, asserted against real pre-fix captures: while
// nvmlDeviceGetVirtualizationMode was a generated stub nvidia-smi rendered
// "N/A", claiming the driver could not tell whether the GPU was virtualized.
func TestVirtualizationModeProblems_RejectsCapturedStubbedDocuments(t *testing.T) {
	for _, fixture := range []string{"qx-a100-healthy.xml", "qx-gb200-healthy.xml"} {
		t.Run(fixture, func(t *testing.T) {
			problems := VirtualizationModeProblems(loadFixture(t, fixture))
			require.Len(t, problems, 2, "both GPUs report a stubbed virtualization_mode")
			assert.Contains(t, strings.Join(problems, "; "), `virtualization_mode = "N/A", want "None"`)
		})
	}
}

// vGPU is out of scope for the mock, so host_vgpu_mode and
// vgpu_heterogeneous_mode must keep reading N/A the way bare-metal hardware
// reports them. A value in either means vGPU state leaked into the answer.
func TestVirtualizationModeProblems_RejectsReportedVGPUState(t *testing.T) {
	out := xmlDocument(
		virtualizationGPU("0000:07:00.0", "None", "Non SR-IOV", "N/A"),
		virtualizationGPU("0000:0F:00.0", "None", "N/A", "Enabled"),
	)

	problems := VirtualizationModeProblems(out)
	require.Len(t, problems, 2)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "host_vgpu_mode")
	assert.Contains(t, joined, "vgpu_heterogeneous_mode")
}

// A GPU-scoped getter that answers for only the first device must be caught.
func TestVirtualizationModeProblems_ChecksEveryGPU(t *testing.T) {
	out := xmlDocument(
		virtualizationGPU("0000:07:00.0", "None", "N/A", "N/A"),
		virtualizationGPU("0000:0F:00.0", "N/A", "N/A", "N/A"),
	)

	problems := VirtualizationModeProblems(out)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "0000:0F:00.0")
}

func TestVirtualizationModeProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := VirtualizationModeProblems("not xml")
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

// pmon reaches the mock through the reverse-engineered internal export table
// and refuses to run there, so its non-zero exit is the baseline rather than a
// failure. Only a crash is (#640, PR #630).
func TestProcessMonitorProblems_AcceptsGracefulRefusalAndSuccess(t *testing.T) {
	assert.Empty(t, ProcessMonitorProblems(255, "Not supported on the device(s)"))
	assert.Empty(t, ProcessMonitorProblems(0, "# gpu        pid  type"))
}

func TestProcessMonitorProblems_RejectsSegfault(t *testing.T) {
	problems := ProcessMonitorProblems(139, "command terminated with exit code 139")
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "139")
}

// A signal death surfaces as 128+N through kubectl, or as a negative code when
// the local kubectl process is itself signalled.
func TestProcessMonitorProblems_RejectsOtherAbnormalExits(t *testing.T) {
	for _, code := range []int{-1, 134, 2} {
		assert.NotEmpty(t, ProcessMonitorProblems(code, "output"), "exit %d", code)
	}
}

// The captured document is the defect: every SRAM reading, the source breakdown
// and the threshold flag are N/A, so a health checker cannot tell a GPU with no
// SRAM errors from one whose SRAM state is unknown (#641).
func TestSramECCProblems_RejectsCapturedNotAvailableReadings(t *testing.T) {
	problems := SramECCProblems(loadFixture(t, "qx-a100-healthy.xml"), SramECCState{})
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "sram_correctable")
	assert.Contains(t, joined, "sram_threshold_exceeded")
	assert.Contains(t, joined, "sram_l2")
}

func TestSramECCProblems_AcceptsZeroedSramReadings(t *testing.T) {
	problems := SramECCProblems(healSramReadings(t), SramECCState{})
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestSramECCProblems_RejectsWrongCounts(t *testing.T) {
	want := SramECCState{Volatile: SramECCCounters{UncorrectableSECDED: 4}}
	problems := SramECCProblems(healSramReadings(t), want)
	require.Len(t, problems, 2, "both GPUs report 0 where 4 is expected")
	assert.Contains(t, problems[0], "volatile sram_uncorrectable_secded = 0, want 4")
}

// The source breakdown must be checked per unit: attributing errors to the SM
// and reading them back on the L2 is a wrong answer, not a rounding difference.
func TestSramECCProblems_RejectsMisattributedSource(t *testing.T) {
	out := strings.ReplaceAll(healSramReadings(t), "<sram_sm>0</sram_sm>", "<sram_sm>4</sram_sm>")

	problems := SramECCProblems(out, SramECCState{Sources: SramECCSources{L2: 4}})
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "sram_l2 = 0, want 4")
	assert.Contains(t, joined, "sram_sm = 4, want 0")
}

func TestSramECCProblems_RejectsThresholdMismatch(t *testing.T) {
	problems := SramECCProblems(healSramReadings(t), SramECCState{ThresholdExceeded: true})
	require.Len(t, problems, 2)
	assert.Contains(t, problems[0], `sram_threshold_exceeded = "No", want "Yes"`)
}

func TestSramECCProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := SramECCProblems("not xml", SramECCState{})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

// row_remapper_histogram read N/A on every profile while the getter was a stub,
// which on Ampere and later means the GPU cannot report remap capacity at all.
func TestRowRemapProblems_RejectsNotAvailableHistogram(t *testing.T) {
	problems := RowRemapProblems(loadFixture(t, "qx-a100-healthy.xml"), RowRemapState{HistogramBanks: 640})
	require.Len(t, problems, 2, "one per GPU")
	assert.Contains(t, problems[0], "row_remapper_histogram")
}

func TestRowRemapProblems_AcceptsPopulatedHistogram(t *testing.T) {
	problems := RowRemapProblems(healSramReadings(t), RowRemapState{HistogramBanks: 640})
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The configured bank count must reach the caller, not just some histogram.
func TestRowRemapProblems_RejectsWrongBankCount(t *testing.T) {
	problems := RowRemapProblems(healSramReadings(t), RowRemapState{HistogramBanks: 1280})
	require.Len(t, problems, 2)
	assert.Contains(t, problems[0], "want it to report 1280 banks")
}

// A profile that configures no remap availability must keep reporting N/A: a
// populated histogram there would mean the mock claims a capability the modelled
// hardware lacks.
func TestRowRemapProblems_RejectsHistogramWhereUnsupportedExpected(t *testing.T) {
	problems := RowRemapProblems(healSramReadings(t), RowRemapState{})
	require.Len(t, problems, 2)
	assert.Contains(t, problems[0], "want N/A")
}

func TestRowRemapProblems_AcceptsUnsupportedHistogram(t *testing.T) {
	problems := RowRemapProblems(loadFixture(t, "qx-a100-healthy.xml"), RowRemapState{})
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestRowRemapProblems_RejectsWrongCountersAndFlags(t *testing.T) {
	want := RowRemapState{Correctable: 1, Pending: true, HistogramBanks: 640}
	problems := RowRemapProblems(healSramReadings(t), want)
	require.Len(t, problems, 4, "count and flag, on each of the two GPUs")
	assert.Contains(t, strings.Join(problems, "; "), "remapped_row_corr = 0, want 1")
	assert.Contains(t, strings.Join(problems, "; "), `remapped_row_pending = "No", want "Yes"`)
}

func TestRowRemapProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := RowRemapProblems("not xml", RowRemapState{})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

// healSramReadings rewrites the captured document into what a GPU that answers
// the SRAM and row-remap getters reports: counters at 0 rather than N/A, the
// threshold flag rendered No, and a populated histogram. It is derived from the
// capture rather than written by hand so the element names stay those of the
// driver's DTD.
func healSramReadings(t *testing.T) string {
	t.Helper()
	out := loadFixture(t, "qx-a100-healthy.xml")
	for _, element := range []string{
		"sram_correctable", "sram_uncorrectable_parity", "sram_uncorrectable_secded",
		"sram_l2", "sram_sm", "sram_microcontroller", "sram_pcie", "sram_other",
	} {
		out = strings.ReplaceAll(out,
			fmt.Sprintf("<%s>N/A</%s>", element, element),
			fmt.Sprintf("<%s>0</%s>", element, element))
	}
	out = strings.ReplaceAll(out,
		"<sram_threshold_exceeded>N/A</sram_threshold_exceeded>",
		"<sram_threshold_exceeded>No</sram_threshold_exceeded>")
	// The bucket elements and their "N bank(s)" bodies are the shapes nvidia-smi
	// 580.65.06 emits once the histogram getter answers.
	return strings.ReplaceAll(out, "<row_remapper_histogram>N/A</row_remapper_histogram>",
		"<row_remapper_histogram>"+
			"<row_remapper_histogram_max>640 bank(s)</row_remapper_histogram_max>"+
			"<row_remapper_histogram_high>0 bank(s)</row_remapper_histogram_high>"+
			"<row_remapper_histogram_partial>0 bank(s)</row_remapper_histogram_partial>"+
			"<row_remapper_histogram_low>0 bank(s)</row_remapper_histogram_low>"+
			"<row_remapper_histogram_none>0 bank(s)</row_remapper_histogram_none>"+
			"</row_remapper_histogram>")
}

// The pre-Ampere rendering is a different set of elements, not different values:
// one combined SRAM Uncorrectable row, no source breakdown, no threshold flag.
// Checking it with the detailed expectation is how the t4 e2e leg failed, so both
// directions are pinned here.
func TestSramECCProblems_AcceptsCombinedLayout(t *testing.T) {
	problems := SramECCProblems(combinedSramReadings(t), SramECCState{Layout: SramECCCombined})
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The combined row carries both uncorrectable flavours, so the expectation is
// their sum.
func TestSramECCProblems_CombinedLayoutSumsUncorrectableFlavours(t *testing.T) {
	out := strings.ReplaceAll(combinedSramReadings(t),
		"<sram_uncorrectable>0</sram_uncorrectable>", "<sram_uncorrectable>6</sram_uncorrectable>")
	counts := SramECCCounters{UncorrectableParity: 2, UncorrectableSECDED: 4}

	want := SramECCState{Volatile: counts, Aggregate: counts, Layout: SramECCCombined}
	assert.Empty(t, SramECCProblems(out, want))

	want.Volatile.UncorrectableSECDED = 3
	problems := SramECCProblems(out, want)
	require.NotEmpty(t, problems)
	assert.Contains(t, problems[0], "sram_uncorrectable = 6, want 5")
}

// Asking for the detailed layout where the driver emits the combined one must
// fail rather than silently pass on absent elements.
func TestSramECCProblems_RejectsCombinedLayoutUnderDetailedExpectation(t *testing.T) {
	problems := SramECCProblems(combinedSramReadings(t), SramECCState{})
	require.NotEmpty(t, problems)
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "sram_uncorrectable_parity")
	assert.Contains(t, joined, "sram_threshold_exceeded")
}

// And the reverse: a GPU that reports the detailed breakdown is not a pre-Ampere
// one, so the combined expectation must not accept it.
func TestSramECCProblems_RejectsDetailedLayoutUnderCombinedExpectation(t *testing.T) {
	problems := SramECCProblems(healSramReadings(t), SramECCState{Layout: SramECCCombined})
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "sram_uncorrectable = \"\"")
}

// combinedSramReadings rewrites the captured document into the pre-Ampere
// rendering nvidia-smi 580.65.06 emits for a Turing GPU: sram_correctable and a
// single sram_uncorrectable per scope, with the source breakdown and the
// threshold flag absent entirely.
func combinedSramReadings(t *testing.T) string {
	t.Helper()
	out := healSramReadings(t)
	out = strings.ReplaceAll(out,
		"<sram_uncorrectable_parity>0</sram_uncorrectable_parity>\n\t\t\t\t"+
			"<sram_uncorrectable_secded>0</sram_uncorrectable_secded>",
		"<sram_uncorrectable>0</sram_uncorrectable>")
	out = strings.ReplaceAll(out,
		"<sram_threshold_exceeded>No</sram_threshold_exceeded>\n\t\t\t", "")
	return regexp.MustCompile(
		`(?s)\s*<aggregate_uncorrectable_sram_sources>.*?</aggregate_uncorrectable_sram_sources>`).
		ReplaceAllString(out, "")
}
