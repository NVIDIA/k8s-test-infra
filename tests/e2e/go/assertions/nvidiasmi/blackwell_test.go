// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The issue #679 defect, verbatim from the mock before the fix: the eight rows
// a real GB300 tray answers N/A, each carrying a value.
const blackwellFabricatedElements = `
		<inforom_version>
			<pwr_object>1.0</pwr_object>
		</inforom_version>
		<gpu_operation_mode>
			<current_gom>All On</current_gom>
			<pending_gom>All On</pending_gom>
		</gpu_operation_mode>
		<sparse_operation_mode>Disabled</sparse_operation_mode>
		<retired_pages>
			<multiple_single_bit_retirement>
				<retired_count>0</retired_count>
			</multiple_single_bit_retirement>
			<double_bit_retirement>
				<retired_count>0</retired_count>
			</double_bit_retirement>
			<pending_blacklist>No</pending_blacklist>
			<pending_retirement>No</pending_retirement>
		</retired_pages>
		<temperature>
			<gpu_target_temperature>85 C</gpu_target_temperature>
		</temperature>`

// blackwellXML renders a one-GPU document carrying the given element lines, so
// a test can state a combination no capture shows.
func blackwellXML(elements string) string {
	return `<?xml version="1.0" ?>
<nvidia_smi_log>
	<attached_gpus>1</attached_gpus>
	<gpu id="0000:0A:00.0">
		<product_name>NVIDIA GB300 NVL</product_name>` + elements + `
	</gpu>
</nvidia_smi_log>`
}

func TestBlackwellRemovedFieldProblems_AcceptsCapturedBlackwellDocument(t *testing.T) {
	problems := BlackwellRemovedFieldProblems(loadFixture(t, "qx-gb300-healthy.xml"), false)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The pre-Blackwell half of the check is the point of it: an assertion that
// only demanded N/A would also pass with the features disabled everywhere,
// which would be a fidelity regression on Turing and Ampere.
func TestBlackwellRemovedFieldProblems_AcceptsCapturedAmpereDocument(t *testing.T) {
	problems := BlackwellRemovedFieldProblems(loadFixture(t, "qx-a100-healthy.xml"), true)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestBlackwellRemovedFieldProblems_RejectsFabricatedValuesOnBlackwell(t *testing.T) {
	problems := BlackwellRemovedFieldProblems(blackwellXML(blackwellFabricatedElements), false)
	require.NotEmpty(t, problems, "pre-fix Blackwell output must fail")
	joined := strings.Join(problems, "; ")
	for _, element := range []string{
		"pwr_object",
		"current_gom",
		"pending_gom",
		"sparse_operation_mode",
		"multiple_single_bit_retirement/retired_count",
		"double_bit_retirement/retired_count",
		"pending_blacklist",
		"gpu_target_temperature",
	} {
		assert.Contains(t, joined, element, "every fabricated row must be named")
	}
}

func TestBlackwellRemovedFieldProblems_RejectsNotAvailableOnPreBlackwell(t *testing.T) {
	problems := BlackwellRemovedFieldProblems(loadFixture(t, "qx-gb300-healthy.xml"), true)
	require.NotEmpty(t, problems, "a pre-Blackwell profile must still report these")
	assert.Contains(t, strings.Join(problems, "; "), "no longer reports")
}

// An N/A body and an absent element are both correct on Blackwell: nvidia-smi
// drops the Supported GPU Target Temp section from -q once the query fails,
// while -q -x keeps the elements with N/A bodies.
func TestBlackwellRemovedFieldProblems_AcceptsAbsentElementsOnBlackwell(t *testing.T) {
	problems := BlackwellRemovedFieldProblems(blackwellXML(""), false)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestBlackwellRemovedFieldProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := BlackwellRemovedFieldProblems("not xml", false)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

// A per-device gate that answers for only one GPU must be caught.
func TestBlackwellRemovedFieldProblems_ChecksEveryGPU(t *testing.T) {
	problems := BlackwellRemovedFieldProblems(loadFixture(t, "qx-gb200-healthy.xml"), false)
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "; "), "0000:0B:00.0",
		"the second GPU must be reported too")
}
