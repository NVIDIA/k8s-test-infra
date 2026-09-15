// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"regexp"
	"sort"
	"strings"
)

// This file carries no build tag, for the same reason as gfd_labels.go: parsing
// the output of the IB diagnostic tools needs nothing but the standard library,
// so keeping it untagged is what makes its tests run in the regular
// `go test ./...` job rather than only under -tags e2e. The cluster-facing scans
// that consume it live in iblinkinfo.go, ibnetdiscover.go and sminfo.go.

// guid16RE matches the 0x-prefixed 64-bit GUIDs the IB diagnostic tools print.
var guid16RE = regexp.MustCompile(`(?i)0x[0-9a-f]{16}`)

// smGUIDRE matches the SM GUID sminfo reports. The width is deliberately
// unbounded: sminfo prints the GUID as a plain hex number, without zero-padding
// it to 16 digits the way ibnetdiscover pads its record headers.
var smGUIDRE = regexp.MustCompile(`(?i)sm guid 0x([0-9a-f]+)`)

// smMasterRE matches how sminfo renders SMState=MASTER(3) — the symbolic name
// on a build that knows it, the numeric state on one that does not.
var smMasterRE = regexp.MustCompile(`(?i)SMINFO_MASTER|state 3`)

// caGUIDRE matches the per-HCA record header ibnetdiscover prints for every
// discovered CA.
var caGUIDRE = regexp.MustCompile(`(?im)^caguid=`)

// ibNetDiscoverForbidden and sminfoForbidden mark a failed query even when the
// tool exits zero, which libibmad-based tools routinely do after the MAD layer
// gave up.
var (
	ibNetDiscoverForbidden = []string{
		"iberror:",
		"can't open UMAD port",
		"mad_rpc",
		"Resource temporarily unavailable",
	}

	sminfoForbidden = []string{
		"iberror:",
		"can't open UMAD port",
		"ibwarn:",
		"mad_rpc",
		"umad_get_mad",
		"Resource temporarily unavailable",
		"query failed",
	}
)

// normGUID reduces the several spellings of one GUID — sysfs colon groups, an
// 0x prefix, upper case — to a single comparable form.
func normGUID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ":", "")
	s = strings.TrimPrefix(s, "0x")
	return s
}

// guidSet collects every 64-bit GUID in out, normalized for comparison.
func guidSet(out string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, m := range guid16RE.FindAllString(out, -1) {
		if g := normGUID(m); g != "" {
			set[g] = struct{}{}
		}
	}
	return set
}

// firstNonLocalGUID returns the lowest GUID in found that local does not claim,
// or "" when the scan reached nothing but the local node's own GUIDs. The mock
// fabric is point-to-point — each CA picks one outbound neighbour, see
// fabric.PeerAtOutbound — so a scan from one pod cannot enumerate every port of
// a larger fabric; reaching any non-local GUID is the cross-pod visibility
// property worth asserting. Sorting keeps a failure reproducible rather than
// naming whichever GUID the map handed back first.
func firstNonLocalGUID(found, local map[string]struct{}) string {
	candidates := make([]string, 0, len(found))
	for g := range found {
		if _, isLocal := local[g]; !isLocal {
			candidates = append(candidates, g)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	return candidates[0]
}

// ibNetDiscoverHasCARecord reports whether the directed-route walk resolved at
// least one HCA. A scan that printed vendor and system attributes but no Ca
// record discovered nothing.
func ibNetDiscoverHasCARecord(out string) bool {
	return caGUIDRE.MatchString(out)
}

// sminfoReportsMaster reports whether sminfo named a master SM.
func sminfoReportsMaster(out string) bool {
	return smMasterRE.MatchString(out)
}

// sminfoMasterGUID returns the normalized SM GUID sminfo reported, or "" when
// the output names none or reports an all-zero one. All-zero is what an
// unanswered SubnGet(SMInfo) leaves in the reply buffer, so it is not a
// believable master identity.
func sminfoMasterGUID(out string) string {
	m := smGUIDRE.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	guid := normGUID(m[1])
	if strings.Trim(guid, "0") == "" {
		return ""
	}
	return guid
}

// firstForbidden returns the first pattern present in out, or "" when out is
// clean.
func firstForbidden(out string, patterns []string) string {
	for _, p := range patterns {
		if strings.Contains(out, p) {
			return p
		}
	}
	return ""
}

// ibNetDiscoverPeer returns the cross-pod GUID the scan reached, or the reason
// the output does not prove cross-pod visibility. A libibmad tool routinely
// exits zero after the MAD layer gave up, so the exit status is not the signal;
// this is.
func ibNetDiscoverPeer(out string, localGUIDs map[string]struct{}) (guid, why string) {
	if p := firstForbidden(out, ibNetDiscoverForbidden); p != "" {
		return "", "ibnetdiscover output contains forbidden pattern " + p
	}
	if !ibNetDiscoverHasCARecord(out) {
		return "", "ibnetdiscover printed no Ca records (caguid=)"
	}
	found := guidSet(out)
	if len(found) == 0 {
		return "", "ibnetdiscover printed no GUIDs"
	}
	cross := firstNonLocalGUID(found, localGUIDs)
	if cross == "" {
		return "", "ibnetdiscover found only local GUIDs, no cross-pod peer"
	}
	return cross, ""
}

// sminfoMaster returns the master SM GUID sminfo reported, or the reason the
// output does not describe a believable master.
func sminfoMaster(out string) (guid, why string) {
	if p := firstForbidden(out, sminfoForbidden); p != "" {
		return "", "sminfo output contains forbidden pattern " + p
	}
	if !sminfoReportsMaster(out) {
		return "", "sminfo did not report a master SM (no SMINFO_MASTER / state 3)"
	}
	g := sminfoMasterGUID(out)
	if g == "" {
		return "", "sminfo did not report a non-zero SM GUID"
	}
	return g, ""
}
