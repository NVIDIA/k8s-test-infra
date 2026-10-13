// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// identityGPU renders one <gpu> element carrying the three identity bodies
// verbatim; an empty body omits the element, which is how a renamed one looks.
func identityGPU(id, addressingMode, pdi, egm string) string {
	element := func(name, body string) string {
		if body == "" {
			return ""
		}
		return "\n\t\t<" + name + ">" + body + "</" + name + ">"
	}
	return `
	<gpu id="` + id + `">
		<product_name>NVIDIA H100 80GB HBM3</product_name>` +
		element("addressing_mode", addressingMode) +
		element("pdi", pdi) + `
		<temperature>
			<gpu_temp>36 C</gpu_temp>
		</temperature>
		<capabilities>` + element("egm", egm) + `
		</capabilities>
	</gpu>`
}

// identityOnDriver580 is the expectation on a driver that has every identity
// query, for the given rendered mode.
func identityOnDriver580(mode string) DeviceIdentityExpectation {
	return DeviceIdentityExpectation{IdentityQueries: true, AddressingMode: mode, EGM: egmDisabled}
}

// Every capture taken on a 580-or-newer driver must pass with the mode its node
// reported, which is what makes the expectation hardware-derived. The A100
// capture is left out: its 570 nvidia-smi renders an addressing mode the
// public NVML header only declares from 580, and emits no pdi element at all.
func TestDeviceIdentityProblems_AcceptsHardwareCaptures(t *testing.T) {
	t.Parallel()
	for file, mode := range map[string]string{
		"qx-b200.xml":  "None",
		"qx-gb200.xml": "ATS",
		"qx-gb300.xml": "ATS",
		"qx-h100.xml":  "HMM",
		"qx-l40s.xml":  "HMM",
		"qx-t4.xml":    "HMM",
	} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			problems := DeviceIdentityProblems(loadHardwareCapture(t, file), identityOnDriver580(mode))
			assert.Empty(t, problems, strings.Join(problems, "; "))
		})
	}
}

// The #1042 regression itself: the mock captures were taken while all three
// getters were generated stubs, so every row read N/A on a driver that answers.
func TestDeviceIdentityProblems_RejectsStubbedReadings(t *testing.T) {
	t.Parallel()
	problems := DeviceIdentityProblems(loadFixture(t, "qx-gb200-healthy.xml"), identityOnDriver580("ATS"))
	require.Len(t, problems, 6, "three rows on each of two GPUs")
	joined := strings.Join(problems, "\n")
	assert.Contains(t, joined, `addressing_mode = "N/A", want "ATS"`)
	assert.Contains(t, joined, `pdi = "N/A"`)
	assert.Contains(t, joined, `capabilities/egm = "N/A", want "disabled"`)
}

// A board whose host reports another mode must fail, so the check cannot be
// satisfied by a mock that answers one constant everywhere.
func TestDeviceIdentityProblems_RejectsAnotherHostsMode(t *testing.T) {
	t.Parallel()
	problems := DeviceIdentityProblems(loadHardwareCapture(t, "qx-h100.xml"), identityOnDriver580("ATS"))
	require.Len(t, problems, 8, "one per GPU")
	assert.Contains(t, problems[0], `addressing_mode = "HMM", want "ATS"`)
}

// Before 580 neither query exists, and nvidia-smi says so rather than N/A; the
// capabilities query arrived in 560, so on older drivers EGM reads N/A. Both
// directions are checked so the gate cannot be satisfied by either rendering.
func TestDeviceIdentityProblems_GatesOnDriver(t *testing.T) {
	t.Parallel()
	preIdentity := xmlDocument(identityGPU("0000:1A:00.0", functionNotFound, functionNotFound, "N/A"))
	before580 := DeviceIdentityExpectation{EGM: "N/A"}

	problems := DeviceIdentityProblems(preIdentity, before580)
	assert.Empty(t, problems, strings.Join(problems, "; "))

	assert.Len(t, DeviceIdentityProblems(preIdentity, identityOnDriver580("HMM")), 3,
		"a driver that has the queries must not render Function Not Found")

	answered := xmlDocument(identityGPU("0000:1A:00.0", "HMM", "0x617befaae11e6abc", "N/A"))
	assert.Len(t, DeviceIdentityProblems(answered, before580), 2,
		"a driver without the queries must not answer them")
}

func TestDeviceIdentityProblems_RejectsMalformedPDI(t *testing.T) {
	t.Parallel()
	for _, pdi := range []string{
		"0x0000000000000000", // an identifier of zero names no GPU
		"617befaae11e6abc",   // missing the 0x prefix nvidia-smi renders
		"0x617b",             // not the full 64 bits
		"N/A",
	} {
		t.Run(pdi, func(t *testing.T) {
			t.Parallel()
			out := xmlDocument(identityGPU("0000:1A:00.0", "HMM", pdi, egmDisabled))
			problems := DeviceIdentityProblems(out, identityOnDriver580("HMM"))
			require.Len(t, problems, 1)
			assert.Contains(t, problems[0], "pdi")
		})
	}
}

// The PDI exists to tell GPUs apart, so two GPUs sharing one is the defect a
// constant answer would produce.
func TestDeviceIdentityProblems_RejectsSharedPDI(t *testing.T) {
	t.Parallel()
	out := xmlDocument(
		identityGPU("0000:1A:00.0", "HMM", "0x617befaae11e6abc", egmDisabled),
		identityGPU("0000:1B:00.0", "HMM", "0x617befaae11e6abc", egmDisabled),
	)
	problems := DeviceIdentityProblems(out, identityOnDriver580("HMM"))
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "0000:1B:00.0 reports pdi 0x617befaae11e6abc, already reported by 0000:1A:00.0")
}

// An absent element is a renamed one, not an empty reading.
func TestDeviceIdentityProblems_ReportsMissingElements(t *testing.T) {
	t.Parallel()
	problems := DeviceIdentityProblems(xmlDocument(identityGPU("0000:1A:00.0", "", "", "")),
		identityOnDriver580("HMM"))
	require.Len(t, problems, 3)
	for _, p := range problems {
		assert.Contains(t, p, "emits no")
	}
}

// A lost GPU renders the NVML error in place of every reading, which says
// nothing about these rows; its healthy siblings are still checked, and a
// document with nothing left to check fails.
func TestDeviceIdentityProblems_SkipsFailedGPU(t *testing.T) {
	t.Parallel()
	lost := `
	<gpu id="0000:1B:00.0">
		<addressing_mode>GPU is lost</addressing_mode>
		<pdi>GPU is lost</pdi>
		<temperature>
			<gpu_temp>GPU is lost</gpu_temp>
		</temperature>
	</gpu>`
	healthy := identityGPU("0000:1A:00.0", "HMM", "0x617befaae11e6abc", egmDisabled)

	problems := DeviceIdentityProblems(xmlDocument(healthy, lost), identityOnDriver580("HMM"))
	assert.Empty(t, problems, strings.Join(problems, "; "))

	problems = DeviceIdentityProblems(xmlDocument(lost), identityOnDriver580("HMM"))
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "every device in the document failed")
}

func TestRenderedAddressingMode(t *testing.T) {
	t.Parallel()
	for configured, want := range map[string]string{
		"hmm":   "HMM",
		" ATS ": "ATS",
		"none":  "None",
		"":      "N/A",
		"hmn":   "N/A", // the mock declines a value it does not recognise
	} {
		assert.Equal(t, want, RenderedAddressingMode(configured), "RenderedAddressingMode(%q)", configured)
	}
}
