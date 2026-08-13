// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package v1alpha1 contains API schema definitions for the mokka.nvidia.com
// API group (SGPUProfile, SGPUInventory, SGPURuntimePolicy). The types are
// consumed by the Mokka Control Plane and rendered into CRD manifests by
// `make gen` (see the mokka-crds Helm chart under deployments/).
//
// +k8s:deepcopy-gen=package
// +groupName=mokka.nvidia.com
// +kubebuilder:object:generate=true
package v1alpha1
