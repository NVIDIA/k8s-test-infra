// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fixtures are `nvidia-smi -q -x` output captured from the mock image against a
// config setting jpeg: 35 / ofa: 12, reduced to the elements under test. The
// buggy one comes from the image built before the NVML getters existed: both
// values are parsed from the config and nvidia-smi still renders N/A, which is
// issue #637.
func utilizationXML(gpus ...string) string {
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
	out := utilizationXML(
		utilizationGPU("0000:07:00.0", jpeg35, ofa12),
		utilizationGPU("0000:0F:00.0", jpeg35, ofa12),
	)
	require.Empty(t, DiffJpgOfaUtilizationXML(out, 35, 12))
}

func TestDiffJpgOfaUtilizationXML_RejectsNotAvailableReadings(t *testing.T) {
	out := utilizationXML(utilizationGPU("0000:07:00.0", jpegNA, ofaNA))

	problems := DiffJpgOfaUtilizationXML(out, 35, 12)
	require.Len(t, problems, 2, "both readings are N/A")
	joined := strings.Join(problems, "\n")
	require.Contains(t, joined, `jpeg_util = "N/A"`)
	require.Contains(t, joined, `ofa_util = "N/A"`)
}

// A zeroed default reading must not satisfy a non-zero expectation, and the two
// values must not be interchangeable — the reason the fixture uses 35 and 12.
func TestDiffJpgOfaUtilizationXML_RejectsWrongPercentages(t *testing.T) {
	out := utilizationXML(utilizationGPU("0000:07:00.0", jpeg35, ofa12))

	require.Len(t, DiffJpgOfaUtilizationXML(out, 0, 0), 2,
		"35 %% / 12 %% must not satisfy a zeroed expectation")
	require.Len(t, DiffJpgOfaUtilizationXML(out, 12, 35), 2,
		"transposed expectations must not pass")
}

// Absent elements decode as empty strings, which must be reported rather than
// read as 0 %.
func TestDiffJpgOfaUtilizationXML_ReportsMissingElements(t *testing.T) {
	out := utilizationXML(utilizationGPU("0000:07:00.0", "", ""))

	problems := DiffJpgOfaUtilizationXML(out, 35, 12)
	require.Len(t, problems, 2)
	joined := strings.Join(problems, "\n")
	require.Contains(t, joined, `jpeg_util = ""`)
	require.Contains(t, joined, `ofa_util = ""`)
}

// A getter that answers for only the first device must fail, and the report must
// name the GPU so the failure points at a device.
func TestDiffJpgOfaUtilizationXML_ChecksEveryGPU(t *testing.T) {
	out := utilizationXML(
		utilizationGPU("0000:07:00.0", jpeg35, ofa12),
		utilizationGPU("0000:0F:00.0", jpegNA, ofa12),
	)

	problems := DiffJpgOfaUtilizationXML(out, 35, 12)
	require.Len(t, problems, 1, "only the second GPU's jpeg_util is N/A")
	require.Contains(t, problems[0], "0000:0F:00.0")
}

// nvidia-smi can die mid-document and still exit with a partial tree on stdout;
// truncated XML must be reported as a parse failure, not silently pass for want
// of any <gpu> elements to check.
func TestDiffJpgOfaUtilizationXML_RejectsTruncatedOutput(t *testing.T) {
	full := utilizationXML(utilizationGPU("0000:07:00.0", jpeg35, ofa12))
	truncated := full[:strings.Index(full, "<utilization>")]

	problems := DiffJpgOfaUtilizationXML(truncated, 35, 12)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "parse nvidia-smi -q -x output")
}

func TestDiffJpgOfaUtilizationXML_RejectsOutputWithoutGPUs(t *testing.T) {
	problems := DiffJpgOfaUtilizationXML(utilizationXML(), 35, 12)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "no GPUs")
}
