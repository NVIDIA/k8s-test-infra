// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeUnhealthy is the block a GPU whose fabric route has failed reports: the
// one faulted condition plus the summary that follows from it, with every
// neighbouring condition inherited from the healthy reference.
func routeUnhealthy() FabricHealthBlock {
	want := HealthyFabricBlock()
	want.RouteUnhealthy = "True"
	want.Summary = "Unhealthy"
	return want
}

// qx-gb200-healthy.xml was captured before #677, so it is the defect itself:
// nvidia-smi rendered every health element as N/A because the mock reported no
// health summary, leaving a consumer unable to tell a healthy fabric from an
// unknown one.
func TestFabricHealthProblems_RejectsUnreportedHealth(t *testing.T) {
	problems := FabricHealthProblems(loadFixture(t, "qx-gb200-healthy.xml"), HealthyFabricBlock())
	require.Len(t, problems, 12, "six unreported elements on each of the two GPUs")
	assert.Contains(t, strings.Join(problems, "; "), `summary = "N/A", want "Healthy"`)
	assert.Contains(t, strings.Join(problems, "; "), `bandwidth = "N/A", want "Full"`)
}

func TestFabricHealthProblems_AcceptsHealthyFabric(t *testing.T) {
	// GPU 1 of this document carries the injected fault, so the healthy
	// expectation is checked against GPU 0 by scoping the other GPU to it.
	out := loadFixture(t, "qx-gb200-fabric-degraded.xml")
	problems := FabricHealthProblemsAt(out, 1, routeUnhealthy(), HealthyFabricBlock())
	assert.Empty(t, problems, strings.Join(problems, "; "))
}

// The assertion that gives the feature its value: one injected condition moves
// its own element and the summary, and nothing else moves. A health mask
// applied wholesale, or a summary pinned without decoding the conditions, would
// satisfy a check that only looked at the faulted element.
func TestFabricHealthProblems_ReportsOnlyTheFaultedCondition(t *testing.T) {
	problems := FabricHealthProblems(loadFixture(t, "qx-gb200-fabric-degraded.xml"), HealthyFabricBlock())
	require.Len(t, problems, 2, "only GPU 1's summary and route element may differ")
	joined := strings.Join(problems, "; ")
	assert.Contains(t, joined, `route_unhealthy = "True", want "False"`)
	assert.Contains(t, joined, `summary = "Unhealthy", want "Healthy"`)
	assert.NotContains(t, joined, "bandwidth")
	assert.NotContains(t, joined, "access_timeout_recovery")
}

// Injection is per device, so a fault reported on the wrong GPU is a defect and
// not a pass: the same document must fail when the fault is expected elsewhere.
func TestFabricHealthProblems_RejectsFaultOnTheWrongGPU(t *testing.T) {
	problems := FabricHealthProblemsAt(loadFixture(t, "qx-gb200-fabric-degraded.xml"),
		0, routeUnhealthy(), HealthyFabricBlock())
	require.NotEmpty(t, problems, "GPU 0 is healthy and GPU 1 is faulted, so neither expectation holds")
	assert.Len(t, problems, 4, "both GPUs report the opposite of what was expected")
}

// A profile with no fabric attachment emits no <fabric> children. Reporting
// that once is clearer than reporting six absent elements, and it must not be
// mistaken for a healthy fabric.
func TestFabricHealthProblems_ReportsAbsentFabric(t *testing.T) {
	out := strings.ReplaceAll(loadFixture(t, "qx-gb200-fabric-degraded.xml"),
		"<state>Completed</state>", "")
	problems := FabricHealthProblems(out, HealthyFabricBlock())
	require.Len(t, problems, 2, "one problem per GPU")
	assert.Contains(t, problems[0], "no fabric state")
}

// An element the driver renamed is reported as absent rather than compared as
// an empty body, matching how the rest of the package reads the document.
func TestFabricHealthProblems_ReportsMissingElement(t *testing.T) {
	out := strings.ReplaceAll(loadFixture(t, "qx-gb200-fabric-degraded.xml"),
		"<bandwidth>Full</bandwidth>", "")
	problems := FabricHealthProblems(out, HealthyFabricBlock())
	assert.Contains(t, strings.Join(problems, "; "), "emits no bandwidth element")
}

func TestFabricHealthProblems_ReportsUnparseableDocument(t *testing.T) {
	problems := FabricHealthProblems("not xml", HealthyFabricBlock())
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "parse nvidia-smi XML")
}
