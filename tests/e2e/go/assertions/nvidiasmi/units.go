// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// `nvidia-smi -q -u`, the S-class chassis query.
//
// Units are the management processors of the S870/S1070 generation. Nothing
// Mokka models has one, and nor does any GPU a modern driver sees, so an empty
// enumeration is the accurate answer. It is also the only one nvidia-smi can
// work with: it calls nvmlUnitGetCount before it touches a unit, and while that
// was a generated stub returning NOT_SUPPORTED the whole subcommand died with
//
//	Unable to determine number of available units: N/A
//
// and exit 3 — a driver too broken to answer, where real hardware reports a
// node with no chassis.
//
// This is the only nvidia-smi surface the fan and cooling work moved. The other
// eight entry points are unreachable from nvidia-smi 575 or 580, which
// reference exactly nvmlDeviceGetFanSpeed and nvmlUnitGetFanSpeedInfo; they are
// covered by the ABI suite in tests/mocknvml instead.

// attachedUnitsRE matches the count row, whose label is padded to a fixed
// column. The body is captured rather than compared so a non-numeric answer can
// be reported as the missing getter it is.
var attachedUnitsRE = regexp.MustCompile(`(?m)^Attached Units\s*:\s*(.+)$`)

// UnitInventoryProblems checks `nvidia-smi -q -u` reports a node with no
// S-class units, rather than failing to find out.
//
// Zero is the point of the check and not a weak expectation: no profile models
// a chassis, and a non-zero count would mean nvidia-smi went on to query a unit
// handle that no enumeration can produce.
func UnitInventoryProblems(out string, exitCode int) []string {
	if exitCode != 0 {
		return []string{fmt.Sprintf(
			"nvidia-smi -q -u exited %d, want 0; a failure here means the driver could not say how "+
				"many S-class units are attached, where real hardware reports none: %s",
			exitCode, strings.TrimSpace(out))}
	}

	match := attachedUnitsRE.FindStringSubmatch(out)
	if match == nil {
		return []string{"nvidia-smi -q -u succeeded but printed no \"Attached Units\" row: " + strings.TrimSpace(out)}
	}

	body := strings.TrimSpace(match[1])
	count, err := strconv.Atoi(body)
	switch {
	case err != nil:
		return []string{fmt.Sprintf(
			"nvidia-smi -q -u reported Attached Units = %q; a non-numeric count means the NVML getter "+
				"behind it is missing or unimplemented", body)}
	case count != 0:
		return []string{fmt.Sprintf(
			"nvidia-smi -q -u reported %d attached unit(s), want 0: no profile models an S-class chassis, "+
				"so nvidia-smi would go on to query a unit handle that cannot exist", count)}
	}
	return nil
}
