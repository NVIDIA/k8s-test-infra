// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strconv"
	"strings"
)

// The platformInfo check, large enough to live beside checks.go rather than in
// it, following temperature.go.

// Rendered peer types. nvidia-smi turns the NVML peerType byte into prose, so
// the expectation is compared against these strings rather than a number.
const (
	peerTypeSwitchConnected = "Switch Connected"
	peerTypeDirectConnected = "Direct Connected"
)

// unsupportedReading is the body nvidia-smi renders for a query the platform
// cannot answer.
const unsupportedReading = "N/A"

// PlatformExpectation is the platform identity a profile configures, in profile
// spelling: PeerType is the YAML value ("switch_connected"), which the check
// translates into what nvidia-smi renders.
//
// ModuleIDs is indexed by device, and every other field is shared by all of the
// node's GPUs — a node occupies one tray, in one slot, of one chassis, so NVML
// reports those as properties of the node and only the module id identifies the
// GPU inside it.
type PlatformExpectation struct {
	ChassisSerialNumber string
	SlotNumber          int
	TrayIndex           int
	HostID              int
	PeerType            string
	ModuleIDs           []int
}

// PlatformIdentityProblems checks the platformInfo block of every GPU. A nil
// want expects the whole block to read N/A, which is correct for every board
// outside a Grace-Blackwell rack and is what keeps this check from being
// satisfiable by hardcoded constants in the mock.
//
// With an expectation it asserts three things: each field carries the
// configured value rather than N/A, the module ids are distinct across the node
// so a physical location identifies one GPU, and the node-level fields agree
// across GPUs. Every field read N/A while nvmlDeviceGetPlatformInfo was a
// generated stub (#642).
//
// A failed GPU is skipped instead of compared, for the reason C2CModeProblems
// documents: platform identity is answered from a handle-lookup path that does
// not tick the failure injector. Skipping every GPU is itself reported.
func PlatformIdentityProblems(out string, want *PlatformExpectation) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	compared := 0
	// Keyed on the parsed number, so only GPUs that actually reported a module
	// id are compared for collisions; a missing or N/A reading is already
	// reported as such by the field comparison.
	moduleOwner := map[int]string{}
	for i := range snap.doc.GPUs {
		gpu := snap.gpu(i)
		if gpu.Failed() {
			continue
		}
		compared++
		got := gpu.PlatformInfo()
		if want == nil {
			problems = append(problems, unsupportedPlatformProblems(gpu.Label(), got)...)
			continue
		}
		problems = append(problems, platformFieldProblems(gpu.Label(), got, *want, i)...)
		moduleID, ok := gpu.ModuleID()
		if !ok {
			continue
		}
		if prev, dup := moduleOwner[moduleID]; dup {
			problems = append(problems, fmt.Sprintf(
				"%s reports module_id %d, already reported by %s; a GPU's physical location must identify it",
				gpu.Label(), moduleID, prev))
		}
		moduleOwner[moduleID] = gpu.Label()
	}
	if compared == 0 {
		return []string{"no GPU had a comparable platformInfo block: every device in the document failed"}
	}
	return problems
}

// unsupportedPlatformProblems reports any field that carries a value on a board
// whose platform cannot report a location. An absent element is a problem too:
// nvidia-smi emits the block on every board, so silence means the driver
// renamed it and the populated case is no longer being checked either.
func unsupportedPlatformProblems(label string, got PlatformInfo) []string {
	var problems []string
	for _, f := range platformFields(got) {
		switch {
		case f.got == "":
			problems = append(problems, fmt.Sprintf(
				"%s emits no %s element, want %q; the driver may have renamed it",
				label, f.name, unsupportedReading))
		case f.got != unsupportedReading:
			problems = append(problems, fmt.Sprintf("%s %s = %q, want %q",
				label, f.name, f.got, unsupportedReading))
		}
	}
	return problems
}

// platformFieldProblems compares one GPU's block against the configured
// identity. The module id is expected per device index, which is the order
// nvidia-smi emits GPUs in; the rest must match the node-level values, so a
// mock that answered per-GPU where the hardware answers per-node fails here.
func platformFieldProblems(label string, got PlatformInfo, want PlatformExpectation, index int) []string {
	wantFields := []struct {
		name string
		want string
	}{
		{"chassis_serial_number", want.ChassisSerialNumber},
		{"slot_number", strconv.Itoa(want.SlotNumber)},
		{"tray_index", strconv.Itoa(want.TrayIndex)},
		{"host_id", strconv.Itoa(want.HostID)},
		{"peer_type", renderedPeerType(want.PeerType)},
		{"module_id", wantModuleID(want.ModuleIDs, index)},
	}

	var problems []string
	for i, f := range platformFields(got) {
		expected := wantFields[i].want
		switch {
		case f.got == "":
			problems = append(problems, fmt.Sprintf(
				"%s emits no %s element, want %q; the driver may have renamed it",
				label, f.name, expected))
		case f.got == unsupportedReading:
			problems = append(problems, fmt.Sprintf(
				"%s %s = %q, want %q; the platform reports no physical location for a GPU that has one",
				label, f.name, unsupportedReading, expected))
		case expected == "":
			problems = append(problems, fmt.Sprintf(
				"%s %s = %q, but the expectation names no module id for device %d",
				label, f.name, f.got, index))
		case f.got != expected:
			problems = append(problems, fmt.Sprintf("%s %s = %q, want %q", label, f.name, f.got, expected))
		}
	}
	return problems
}

// platformFields pairs each element name with its body, in one order both
// comparisons walk.
func platformFields(got PlatformInfo) []struct {
	name string
	got  string
} {
	return []struct {
		name string
		got  string
	}{
		{"chassis_serial_number", got.ChassisSerialNumber},
		{"slot_number", got.SlotNumber},
		{"tray_index", got.TrayIndex},
		{"host_id", got.HostID},
		{"peer_type", got.PeerType},
		{"module_id", got.ModuleID},
	}
}

// wantModuleID is the module id configured for a device index, or "" when the
// expectation names none — which platformFieldProblems reports rather than
// silently passing, since a document with more GPUs than the profile describes
// would otherwise go unchecked.
func wantModuleID(moduleIDs []int, index int) string {
	if index < 0 || index >= len(moduleIDs) {
		return ""
	}
	return strconv.Itoa(moduleIDs[index])
}

// renderedPeerType translates a profile's peer_type into what nvidia-smi prints.
// Anything the engine does not recognise as switch-connected resolves to direct,
// so the two sides agree on the default.
func renderedPeerType(configured string) string {
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case "switch_connected", "switchconnected", "switch":
		return peerTypeSwitchConnected
	default:
		return peerTypeDirectConnected
	}
}
