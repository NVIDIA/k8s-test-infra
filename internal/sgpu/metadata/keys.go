// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package metadata defines the Kubernetes metadata surface owned by Mokka.
package metadata

import "slices"

//nolint:revive // These keys are the cohesive public projection metadata contract.
const (
	// AssignedLabel marks a Kubernetes Node with an active exact logical rack Node assignment.
	AssignedLabel        = "mokka.nvidia.com/sgpu-assigned"
	CliqueLabel          = "nvidia.com/gpu.clique"
	AssignmentAnnotation = "mokka.nvidia.com/sgpu-assignment"
)

// ProjectionLabelKeys returns the labels whose values are derived from a rack
// binding and therefore cannot safely drive placement.
func ProjectionLabelKeys() []string {
	return []string{AssignedLabel, CliqueLabel}
}

// LabelsEqualIgnoringProjection reports whether two Node label sets match
// apart from the projection labels. Mokka writes those from rack bindings, so
// a change confined to them cannot alter placement.
func LabelsEqualIgnoringProjection(a, b map[string]string) bool {
	projection := ProjectionLabelKeys()
	for key, value := range a {
		if slices.Contains(projection, key) {
			continue
		}
		if other, exists := b[key]; !exists || other != value {
			return false
		}
	}
	for key := range b {
		if slices.Contains(projection, key) {
			continue
		}
		if _, exists := a[key]; !exists {
			return false
		}
	}
	return true
}
