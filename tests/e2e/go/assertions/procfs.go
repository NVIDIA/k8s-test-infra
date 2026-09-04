// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/profile"
)

// This file carries no build tag on purpose, for the reason spelled out in
// gfd_labels.go: the parse below needs nothing but the standard library, so
// keeping it untagged is what makes its tests run in the regular
// `go test ./...` job rather than only under -tags e2e. The cluster-facing
// assertion that consumes it lives in procfs_exec.go.

// ParamsPath is the staged /proc/driver/nvidia/params as it is readable from
// inside the nvml-mock pod. The tree is staged at the overlay root and not at
// the kernel path: runc refuses any bind mount whose target is inside /proc, so
// unlike the PCI sysfs tree this surface cannot be served where a consumer
// would look for it (#792).
const ParamsPath = "/var/lib/nvml-mock/driver/proc/driver/nvidia/params"

// paramsNameFieldWidth is the %31[^:] field width in nvidia-modprobe's scan.
const paramsNameFieldWidth = 31

// deviceFileParamKeys are the four keys nvidia-modprobe acts on, under the
// names it matches — unprefixed, since NVreg_ is the modprobe parameter name
// and procfs reports the resolved value under the bare name.
var deviceFileParamKeys = []string{
	"ModifyDeviceFiles",
	"DeviceFileUID",
	"DeviceFileGID",
	"DeviceFileMode",
}

// paramsReachableByNvidiaModprobe returns the keys a real consumer can read out
// of a params file, by running the loop nvidia-modprobe runs over it
// (modprobe-utils/nvidia-modprobe-utils.c):
//
//	while (fscanf(fp, "%31[^:]: %u\n", name, &value) == 2)
//
// The loop stops at the first line it cannot consume whole, and every key below
// that line is unreachable however well-formed it looks. Two things stop it: a
// value that is not a bare unsigned integer, and a name past the field width,
// whose tail is left in the stream so the following ": " never matches.
//
// Reproducing the parser is what makes the check honest. Grepping the file
// would pass on a params file no consumer can read, which is exactly the state
// this surface shipped in.
//
// The agent's own tests carry a second copy of this loop. That is deliberate:
// this suite asserts against the deployed artifact and imports nothing from
// internal/, so sharing the parser would let one mistake satisfy both layers.
func paramsReachableByNvidiaModprobe(content string) map[string]uint64 {
	reachable := map[string]uint64{}

	for line := range strings.SplitSeq(content, "\n") {
		name, rawValue, found := strings.Cut(line, ":")
		if !found || len(name) > paramsNameFieldWidth {
			return reachable
		}

		value, err := strconv.ParseUint(strings.TrimSpace(rawValue), 10, 64)
		if err != nil {
			return reachable
		}

		reachable[name] = value
	}

	return reachable
}

// ParamsProblems reports every way a staged params file departs from what
// nvidia-modprobe needs to honour want. An empty result means the file works.
//
// Unreachable keys and wrong values are reported separately, and every problem
// is collected, so a file that stranded three keys cannot look like a single
// typo — the ordering defect strands keys in bulk, and seeing that is the point.
func ParamsProblems(content string, want profile.DeviceFileParams) []string {
	reachable := paramsReachableByNvidiaModprobe(content)

	wantValues := map[string]uint64{
		"ModifyDeviceFiles": boolParam(want.ModifyDeviceFiles),
		"DeviceFileUID":     uint64(want.UID),  //nolint:gosec // profile-authored, non-negative
		"DeviceFileGID":     uint64(want.GID),  //nolint:gosec // profile-authored, non-negative
		"DeviceFileMode":    uint64(want.Mode), //nolint:gosec // profile-authored, non-negative
	}

	var problems []string
	for _, key := range deviceFileParamKeys {
		got, ok := reachable[key]
		switch {
		case !ok:
			problems = append(problems, key+": nvidia-modprobe cannot reach this key")
		case got != wantValues[key]:
			problems = append(problems, fmt.Sprintf("%s: %d, want %d", key, got, wantValues[key]))
		}
	}

	return problems
}

// boolParam renders a bool the way the driver reports it in params.
func boolParam(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}
