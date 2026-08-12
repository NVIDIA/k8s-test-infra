// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// This file carries no build tag on purpose — same rationale as
// nvidiasmi_temperature.go. DiffJpgOfaUtilizationXML is pure parsing so it
// unit-tests without a cluster; the kubectl exec wrapper lives in nvidiasmi.go
// under //go:build e2e.

// smiUtilizationLog decodes just the utilization block of each GPU in
// `nvidia-smi -q -x` output. The XML is the machine-readable form of `-q`, so
// it is matched against its DTD element names rather than the column layout of
// the human-readable table. Readings are strings because nvidia-smi renders
// both "35 %" and "N/A" in the same element.
type smiUtilizationLog struct {
	GPUs []struct {
		ID          string `xml:"id,attr"`
		Utilization struct {
			JPEG string `xml:"jpeg_util"`
			OFA  string `xml:"ofa_util"`
		} `xml:"utilization"`
	} `xml:"gpu"`
}

// DiffJpgOfaUtilizationXML checks the jpeg_util and ofa_util elements of
// `nvidia-smi -q -x` against the configured utilization.jpeg and
// utilization.ofa percentages, for every GPU in the output. A GPU-scoped
// getter that answers for only one device is therefore caught.
//
// An "N/A" reading is the defect this exists to catch (#637): both values were
// parsed from the config and then dropped, because the NVML entry points were
// generated stubs returning NOT_SUPPORTED.
func DiffJpgOfaUtilizationXML(out string, wantJPEG, wantOFA int) []string {
	var log smiUtilizationLog
	if err := xml.Unmarshal([]byte(out), &log); err != nil {
		return []string{fmt.Sprintf("parse nvidia-smi -q -x output: %v", err)}
	}
	if len(log.GPUs) == 0 {
		return []string{"nvidia-smi -q -x reported no GPUs"}
	}

	var problems []string
	for _, gpu := range log.GPUs {
		rows := []struct {
			element string
			reading string
			want    int
		}{
			{"jpeg_util", gpu.Utilization.JPEG, wantJPEG},
			{"ofa_util", gpu.Utilization.OFA, wantOFA},
		}
		for _, row := range rows {
			pct, ok := parseUtilizationPercent(row.reading)
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"GPU %s %s = %q, want %d %%; a non-numeric reading means the NVML getter is missing or unimplemented",
					gpu.ID, row.element, row.reading, row.want))
				continue
			}
			if pct != row.want {
				problems = append(problems, fmt.Sprintf("GPU %s %s = %d %%, want %d %%",
					gpu.ID, row.element, pct, row.want))
			}
		}
	}
	return problems
}

// parseUtilizationPercent reads a "35 %" style element body. An empty body
// (element absent) and "N/A" both fail, which is what the caller reports.
func parseUtilizationPercent(reading string) (int, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(reading), "%"))
	pct, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, false
	}
	return pct, true
}
