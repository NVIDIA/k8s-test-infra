// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"regexp"
	"strings"
)

// functionNotFound is what nvidia-smi renders for a query whose NVML symbol
// the driver does not export, as opposed to N/A for one the device declines.
const functionNotFound = "Function Not Found"

// egmDisabled is the capabilities/egm body of every captured board: EGM has to
// be turned on in the kernel module, Grace hosts included.
const egmDisabled = "disabled"

// pdiBody is how nvidia-smi renders a Per Device Identifier: 0x and all 64 bits.
var pdiBody = regexp.MustCompile(`^0x[0-9a-fA-F]{16}$`)

// DeviceIdentityExpectation is what the identity rows of `nvidia-smi -q` should
// read, as nvidia-smi renders them.
type DeviceIdentityExpectation struct {
	// IdentityQueries is whether the driver exports nvmlDeviceGetAddressingMode
	// and nvmlDeviceGetPdi. Without them both rows read Function Not Found.
	IdentityQueries bool
	// AddressingMode is the rendered mode, compared when IdentityQueries holds.
	AddressingMode string
	// EGM is the capabilities/egm body.
	EGM string
}

// RenderedAddressingMode translates a profile's addressing_mode into what
// nvidia-smi prints. Anything the mock does not recognise it declines, which
// renders as N/A, so the two sides agree on a misspelt value too.
func RenderedAddressingMode(configured string) string {
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case "hmm":
		return "HMM"
	case "ats":
		return "ATS"
	case "none":
		return "None"
	default:
		return unsupportedReading
	}
}

// DeviceIdentityProblems checks the Addressing Mode, PDI and EGM rows of every
// GPU. All three read N/A while the getters behind them were generated stubs
// (#1042), which a driver that has the queries never renders.
//
// The PDI is checked for shape and uniqueness rather than a value: it is a
// fused identifier with no profile key, and what makes it useful to a consumer
// is that it tells the node's GPUs apart.
//
// A failed GPU is skipped instead of compared, for the reason C2CModeProblems
// documents. Skipping every GPU is itself reported.
func DeviceIdentityProblems(out string, want DeviceIdentityExpectation) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	wantMode := functionNotFound
	if want.IdentityQueries {
		wantMode = want.AddressingMode
	}

	var problems []string
	compared := 0
	pdiOwner := map[string]string{}
	for i := range snap.doc.GPUs {
		gpu := snap.gpu(i)
		if gpu.Failed() {
			continue
		}
		compared++
		label, el := gpu.Label(), gpu.element
		problems = append(problems, bodyProblems(label, "addressing_mode", el.AddressingMode, wantMode)...)
		if want.IdentityQueries {
			problems = append(problems, pdiProblems(label, el.PDI, pdiOwner)...)
		} else {
			problems = append(problems, bodyProblems(label, "pdi", el.PDI, functionNotFound)...)
		}
		problems = append(problems, bodyProblems(label, "capabilities/egm", el.Capabilities.EGM, want.EGM)...)
	}
	if compared == 0 {
		return []string{"no GPU had comparable identity rows: every device in the document failed"}
	}
	return problems
}

func bodyProblems(label, name string, got reading, want string) []string {
	body := strings.TrimSpace(string(got))
	switch {
	case !got.present():
		return []string{fmt.Sprintf("%s emits no %s element, want %q; the driver may have renamed it",
			label, name, want)}
	case body != want:
		return []string{fmt.Sprintf("%s %s = %q, want %q", label, name, body, want)}
	}
	return nil
}

// pdiProblems checks a PDI is a non-zero 64-bit identifier no other GPU in the
// document reported, recording it in owner for the GPUs after it.
func pdiProblems(label string, got reading, owner map[string]string) []string {
	body := strings.TrimSpace(string(got))
	switch {
	case !got.present():
		return []string{label + " emits no pdi element; the driver may have renamed it"}
	case !pdiBody.MatchString(body):
		return []string{fmt.Sprintf("%s pdi = %q, want a 64-bit identifier rendered as 0x and 16 hex digits",
			label, body)}
	case strings.Trim(body[2:], "0") == "":
		return []string{fmt.Sprintf("%s pdi = %q; an identifier of zero names no GPU", label, body)}
	}
	key := strings.ToLower(body)
	if prev, dup := owner[key]; dup {
		return []string{fmt.Sprintf("%s reports pdi %s, already reported by %s; the identifier must tell GPUs apart",
			label, body, prev)}
	}
	owner[key] = label
	return nil
}
