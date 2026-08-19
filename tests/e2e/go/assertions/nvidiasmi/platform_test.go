// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gb200Platform is what the gb200 chart profile configures, and what the
// captured fixtures carry: one node in one tray of one chassis, with a module
// id per GPU.
func gb200Platform() *PlatformExpectation {
	return &PlatformExpectation{
		ChassisSerialNumber: "1822725100200",
		SlotNumber:          21,
		TrayIndex:           11,
		HostID:              1,
		PeerType:            "switch_connected",
		ModuleIDs:           []int{1, 2},
	}
}

// platformGPU builds a <gpu> whose platformInfo carries the given bodies. The
// temperature body drives Failed(), so a caller can build a lost device.
func platformGPU(id string, p PlatformInfo, gpuTemp string) string {
	return `	<gpu id="` + id + `">
		<platformInfo>
			<chassis_serial_number>` + p.ChassisSerialNumber + `</chassis_serial_number>
			<slot_number>` + p.SlotNumber + `</slot_number>
			<tray_index>` + p.TrayIndex + `</tray_index>
			<host_id>` + p.HostID + `</host_id>
			<peer_type>` + p.PeerType + `</peer_type>
			<module_id>` + p.ModuleID + `</module_id>
		</platformInfo>
		<temperature>
			<gpu_temp>` + gpuTemp + `</gpu_temp>
		</temperature>
	</gpu>`
}

// populatedPlatform is the block a GPU on the gb200 profile renders.
func populatedPlatform(moduleID string) PlatformInfo {
	return PlatformInfo{
		ChassisSerialNumber: "1822725100200",
		SlotNumber:          "21",
		TrayIndex:           "11",
		HostID:              "1",
		PeerType:            "Switch Connected",
		ModuleID:            moduleID,
	}
}

func unsupportedPlatform() PlatformInfo {
	return PlatformInfo{
		ChassisSerialNumber: "N/A", SlotNumber: "N/A", TrayIndex: "N/A",
		HostID: "N/A", PeerType: "N/A", ModuleID: "N/A",
	}
}

func TestPlatformIdentityProblems_AcceptsCapturedGraceProfile(t *testing.T) {
	problems := PlatformIdentityProblems(loadFixture(t, "qx-gb200-healthy.xml"), gb200Platform())
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

func TestPlatformIdentityProblems_AcceptsNotAvailableOnNonGraceProfile(t *testing.T) {
	problems := PlatformIdentityProblems(loadFixture(t, "qx-a100-healthy.xml"), nil)
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The #642 regression itself: the stubbed entry point made every board read N/A,
// including the NVL72 ones whose whole point is a physical location.
func TestPlatformIdentityProblems_RejectsNotAvailableWhenLocationExpected(t *testing.T) {
	problems := PlatformIdentityProblems(loadFixture(t, "qx-a100-healthy.xml"), gb200Platform())
	require.Len(t, problems, 12, "six fields on each of two GPUs")
	assert.Contains(t, strings.Join(problems, "; "), "the platform reports no physical location")
}

// The other direction, which keeps the fix from being "always report a
// location": a board that cannot report one must not satisfy the check.
func TestPlatformIdentityProblems_RejectsLocationWhenNotAvailableExpected(t *testing.T) {
	problems := PlatformIdentityProblems(loadFixture(t, "qx-gb200-healthy.xml"), nil)
	require.Len(t, problems, 12, "six fields on each of two GPUs")
	assert.Contains(t, strings.Join(problems, "; "), `chassis_serial_number = "1822725100200", want "N/A"`)
}

// A shared module id is the failure mode the whole check exists for: a fix that
// answered one constant for every GPU would render a location that cannot say
// which board failed.
func TestPlatformIdentityProblems_RejectsSharedModuleID(t *testing.T) {
	out := xmlDocument(
		platformGPU("0000:0A:00.0", populatedPlatform("1"), "36 C"),
		platformGPU("0000:0B:00.0", populatedPlatform("1"), "36 C"),
	)
	problems := PlatformIdentityProblems(out, gb200Platform())
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, "already reported by")
	assert.Contains(t, joined, `module_id = "1", want "2"`)
}

// Each field is compared against the profile, not merely checked for presence,
// so a plausible-looking wrong value fails.
func TestPlatformIdentityProblems_RejectsWrongFieldValues(t *testing.T) {
	for name, mutate := range map[string]func(p *PlatformInfo){
		"chassis": func(p *PlatformInfo) { p.ChassisSerialNumber = "1822725100999" },
		"slot":    func(p *PlatformInfo) { p.SlotNumber = "22" },
		"tray":    func(p *PlatformInfo) { p.TrayIndex = "12" },
		"host":    func(p *PlatformInfo) { p.HostID = "2" },
		"peer":    func(p *PlatformInfo) { p.PeerType = "Direct Connected" },
		"module":  func(p *PlatformInfo) { p.ModuleID = "5" },
	} {
		t.Run(name, func(t *testing.T) {
			got := populatedPlatform("1")
			mutate(&got)
			problems := PlatformIdentityProblems(
				xmlDocument(platformGPU("0000:0A:00.0", got, "36 C")),
				&PlatformExpectation{
					ChassisSerialNumber: "1822725100200", SlotNumber: 21, TrayIndex: 11,
					HostID: 1, PeerType: "switch_connected", ModuleIDs: []int{1},
				})
			require.Len(t, problems, 1)
			assert.Contains(t, problems[0], "want")
		})
	}
}

// The node-level fields must agree across GPUs, since a node sits in exactly one
// tray of one chassis. A mock answering them per GPU fails here.
func TestPlatformIdentityProblems_RejectsPerGPUNodeFields(t *testing.T) {
	second := populatedPlatform("2")
	second.TrayIndex = "12"
	out := xmlDocument(
		platformGPU("0000:0A:00.0", populatedPlatform("1"), "36 C"),
		platformGPU("0000:0B:00.0", second, "36 C"),
	)
	problems := PlatformIdentityProblems(out, gb200Platform())
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], `tray_index = "12", want "11"`)
}

// An absent element is reported as such rather than compared as an empty body,
// because that is what a driver renaming the element looks like. Both directions
// report it: on a non-Grace profile the check would otherwise pass vacuously.
func TestPlatformIdentityProblems_ReportsMissingElement(t *testing.T) {
	got := populatedPlatform("1")
	got.ModuleID = ""
	out := xmlDocument(platformGPU("0000:0A:00.0", got, "36 C"))

	problems := PlatformIdentityProblems(out, &PlatformExpectation{
		ChassisSerialNumber: "1822725100200", SlotNumber: 21, TrayIndex: 11,
		HostID: 1, PeerType: "switch_connected", ModuleIDs: []int{1},
	})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "emits no module_id element")

	blank := xmlDocument(platformGPU("0000:0A:00.0", PlatformInfo{}, "36 C"))
	problems = PlatformIdentityProblems(blank, nil)
	require.Len(t, problems, 6, "every absent element is reported")
	assert.Contains(t, problems[0], "emits no chassis_serial_number element")
}

// A document with more GPUs than the profile describes must not pass for want of
// an expected module id.
func TestPlatformIdentityProblems_ReportsUnexpectedExtraGPU(t *testing.T) {
	out := xmlDocument(
		platformGPU("0000:0A:00.0", populatedPlatform("1"), "36 C"),
		platformGPU("0000:0B:00.0", populatedPlatform("2"), "36 C"),
	)
	problems := PlatformIdentityProblems(out, &PlatformExpectation{
		ChassisSerialNumber: "1822725100200", SlotNumber: 21, TrayIndex: 11,
		HostID: 1, PeerType: "switch_connected", ModuleIDs: []int{1},
	})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "names no module id for device 1")
}

// A lost GPU is not evidence either way, for the reason C2CModeProblems
// documents. The healthy siblings are still checked.
func TestPlatformIdentityProblems_SkipsFailedGPU(t *testing.T) {
	out := xmlDocument(
		platformGPU("0000:0A:00.0", populatedPlatform("1"), "36 C"),
		platformGPU("0000:0B:00.0", unsupportedPlatform(), "GPU is lost"),
	)
	assert.Empty(t, PlatformIdentityProblems(out, gb200Platform()))

	wrongHealthy := xmlDocument(
		platformGPU("0000:0A:00.0", unsupportedPlatform(), "36 C"),
		platformGPU("0000:0B:00.0", unsupportedPlatform(), "GPU is lost"),
	)
	problems := PlatformIdentityProblems(wrongHealthy, gb200Platform())
	require.Len(t, problems, 6, "the healthy GPU is still compared")
	assert.Contains(t, problems[0], "0000:0A:00.0")
}

// The captured lost document must pass as-is: GPU 0 is healthy and GPU 1 is
// skipped, so the failure-injection scenario needs no separate expectation.
func TestPlatformIdentityProblems_AcceptsCapturedLostDocument(t *testing.T) {
	problems := PlatformIdentityProblems(loadFixture(t, "qx-gb200-lost.xml"), gb200Platform())
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// Skipping failed GPUs must not let a document where every device failed pass
// for want of anything to compare.
func TestPlatformIdentityProblems_RejectsDocumentWhereEveryGPUFailed(t *testing.T) {
	out := xmlDocument(
		platformGPU("0000:0A:00.0", unsupportedPlatform(), "GPU is lost"),
		platformGPU("0000:0B:00.0", unsupportedPlatform(), "GPU is lost"),
	)
	problems := PlatformIdentityProblems(out, gb200Platform())
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "every device in the document failed")
}

func TestPlatformIdentityProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := PlatformIdentityProblems("not xml", gb200Platform())
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}

// The rendering must match nvidia-smi's own vocabulary, which is prose rather
// than the NVML byte or the profile spelling.
func TestRenderedPeerType(t *testing.T) {
	for in, want := range map[string]string{
		"switch_connected":   peerTypeSwitchConnected,
		" Switch_Connected ": peerTypeSwitchConnected,
		"switch":             peerTypeSwitchConnected,
		"direct_connected":   peerTypeDirectConnected,
		"":                   peerTypeDirectConnected,
		"nonsense":           peerTypeDirectConnected,
	} {
		assert.Equal(t, want, renderedPeerType(in), "peer_type %q", in)
	}
}

// ModuleID is the accessor a consumer deriving a physical position uses, so an
// unsupported board must not read as module 0.
func TestGPUModuleID(t *testing.T) {
	snap, err := ParseSnapshot(loadFixture(t, "qx-gb200-healthy.xml"))
	require.NoError(t, err)
	gpu, err := snap.GPU(1)
	require.NoError(t, err)
	id, ok := gpu.ModuleID()
	require.True(t, ok, "gb200 GPU 1 reports a module id")
	assert.Equal(t, 2, id)

	snap, err = ParseSnapshot(loadFixture(t, "qx-a100-healthy.xml"))
	require.NoError(t, err)
	gpu, err = snap.GPU(0)
	require.NoError(t, err)
	_, ok = gpu.ModuleID()
	assert.False(t, ok, "a100 reports no module id")
}
