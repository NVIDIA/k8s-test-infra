// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"strings"
)

// The fabric health checks, kept out of checks.go because they compare a block
// of related readings per GPU and because the injection check compares two
// different expectations within one document.

// FabricHealthBlock is the <fabric><health> block as nvidia-smi renders it.
// Bodies are compared as text rather than decoded: what an operator and a
// fault-handling controller read is exactly this text, and "N/A" — the
// pre-#677 rendering of every element — is a meaningful value to assert
// against, not a missing one.
type FabricHealthBlock struct {
	Summary                string
	Bandwidth              string
	RouteRecovery          string
	RouteUnhealthy         string
	AccessTimeoutRecovery  string
	IncorrectConfiguration string
}

// HealthyFabricBlock is the block a fabric-attached GPU in a healthy rack reports,
// taken element for element from a real GB300 tray (driver 580.173.02).
func HealthyFabricBlock() FabricHealthBlock {
	return FabricHealthBlock{
		Summary:                "Healthy",
		Bandwidth:              "Full",
		RouteRecovery:          "False",
		RouteUnhealthy:         "False",
		AccessTimeoutRecovery:  "False",
		IncorrectConfiguration: "None",
	}
}

// FabricHealthProblems checks every GPU's fabric health block against want.
func FabricHealthProblems(out string, want FabricHealthBlock) []string {
	return fabricHealthProblems(out, func(int) FabricHealthBlock { return want })
}

// FabricHealthProblemsAt checks the GPU at index against want and every other
// GPU against others. Runtime injection is per device, so a fault that leaks
// onto the node's other GPUs — or one applied to the wrong device — is a
// defect this reports rather than one it passes over.
func FabricHealthProblemsAt(out string, index int, want, others FabricHealthBlock) []string {
	return fabricHealthProblems(out, func(i int) FabricHealthBlock {
		if i == index {
			return want
		}
		return others
	})
}

func fabricHealthProblems(out string, want func(int) FabricHealthBlock) []string {
	snap, err := ParseSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.doc.GPUs {
		problems = append(problems, fabricBlockProblems(gpu.label(i), gpu.Fabric, want(i))...)
	}
	return problems
}

// fabricBlockProblems reports one GPU's differences, one element per problem,
// so a failure names the condition that regressed instead of dumping the block.
func fabricBlockProblems(name string, got fabricBlock, want FabricHealthBlock) []string {
	// A GPU with no fabric at all emits no <fabric> children; reporting that
	// once is clearer than reporting six absent elements.
	if !got.State.present() {
		return []string{name + " emits no fabric state: the device reports no fabric attachment"}
	}

	var problems []string
	for _, element := range []struct {
		name string
		got  reading
		want string
	}{
		{"summary", got.Health.Summary, want.Summary},
		{"bandwidth", got.Health.Bandwidth, want.Bandwidth},
		{"route_recovery_in_progress", got.Health.RouteRecovery, want.RouteRecovery},
		{"route_unhealthy", got.Health.RouteUnhealthy, want.RouteUnhealthy},
		{"access_timeout_recovery", got.Health.AccessTimeoutRecovery, want.AccessTimeoutRecovery},
		{"incorrect_configuration", got.Health.IncorrectConfiguration, want.IncorrectConfiguration},
	} {
		body := strings.TrimSpace(string(element.got))
		switch {
		case !element.got.present():
			problems = append(problems, fmt.Sprintf(
				"%s fabric health emits no %s element, want %q; the driver may have renamed it",
				name, element.name, element.want))
		case !strings.EqualFold(body, element.want):
			problems = append(problems, fmt.Sprintf("%s fabric health %s = %q, want %q",
				name, element.name, body, element.want))
		}
	}
	return problems
}
