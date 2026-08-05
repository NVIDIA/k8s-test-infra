// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import "fmt"

// This file carries no build tag on purpose, for the reason spelled out in
// gfd_labels.go: the contract below needs nothing but the standard library, so
// keeping it untagged is what makes its tests run in the regular
// `go test ./...` job rather than only under -tags e2e. The cluster-facing
// assertion that consumes it lives in configmaps.go.

// The fake-gpu-operator's discovery contract, mirrored from
// run-ai/fake-gpu-operator internal/common/profile/constants.go. Their loader
// (internal/common/profile/profile.go) does
// `ConfigMaps(ns).Get(CmNamePrefix + name)` and then reads `CmProfileKey`, so
// the name and the data key are each independently fatal to the lookup. The
// label is NOT on that code path — it is declared and used only by their own
// chart and tests — but it is their published discovery label, so we emit and
// assert it to keep the artifact self-describing.
const (
	FGOConfigMapNamePrefix = "gpu-profile-"
	FGOProfileDataKey      = "profile.yaml"
	FGODiscoveryLabel      = "fake-gpu-operator/gpu-profile"
)

// FGOProfileConfigMapName returns the ConfigMap name the fake-gpu-operator's
// loader Gets for a profile. Built the same way their loader builds it.
func FGOProfileConfigMapName(profile string) string {
	return FGOConfigMapNamePrefix + profile
}

// DiffFGOProfileConfigMap reports every way a rendered ConfigMap departs from
// the fake-gpu-operator's discovery contract. An empty result means it matches.
//
// Name, label and data key are checked separately and all problems are
// returned, so a drift in one field cannot be masked by another field still
// being correct.
func DiffFGOProfileConfigMap(profile, name string, labels, data map[string]string) []string {
	var problems []string

	if want := FGOProfileConfigMapName(profile); name != want {
		problems = append(problems, fmt.Sprintf("name is %q, want %q", name, want))
	}

	switch value, present := labels[FGODiscoveryLabel]; {
	case !present || value == "":
		problems = append(problems, fmt.Sprintf("label %s missing (want %q)", FGODiscoveryLabel, "true"))
	case value != "true":
		problems = append(problems, fmt.Sprintf("label %s = %q, want %q", FGODiscoveryLabel, value, "true"))
	}

	switch body, present := data[FGOProfileDataKey]; {
	case !present:
		problems = append(problems, fmt.Sprintf("data key %q missing", FGOProfileDataKey))
	case body == "":
		problems = append(problems, fmt.Sprintf("data key %q is empty", FGOProfileDataKey))
	}

	return problems
}
