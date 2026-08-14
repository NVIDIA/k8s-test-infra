// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
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
