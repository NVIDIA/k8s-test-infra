// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

// Package metadata defines the Kubernetes metadata surface owned by Mokka.
package metadata

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
