// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SGPURuntimePolicy applies a sparse RuntimeState override to a subset of the
// referenced SGPUInventory: a whole inventory, specific rack groups, specific
// rack indices, specific node indices, or specific GPU indices. Omitted
// spec.runtime fields inherit from the profile's defaults.
//
// Print columns render the full targeting coordinate — inventory, then each
// optional narrowing level (rack groups, rack indexes, node indexes, GPU
// indexes). Kind is pinned to SGPUInventory by the enum validation on
// TargetRef.Kind, so we skip a redundant Kind column. Empty cells naturally
// read as "all" at that level, matching MEP0001's sparse-override semantics.
//
// Inventory binds to spec (scalar; visible immediately). The four narrowing
// columns bind to controller-populated .status.*Summary strings so they
// render EndpointSlice-style ("training,ci" / "0,1,2") without JSON
// brackets — CRD printer-column JSONPath cannot join arrays, so the
// reconciler pre-joins each axis. Columns stay blank until the controller
// reconciles.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka,shortName=srpol
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Inventory",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="RackGroups",type=string,JSONPath=`.status.rackGroupsSummary`
// +kubebuilder:printcolumn:name="Racks",type=string,JSONPath=`.status.rackIndexesSummary`
// +kubebuilder:printcolumn:name="Nodes",type=string,JSONPath=`.status.nodeIndexesSummary`
// +kubebuilder:printcolumn:name="GPUs",type=string,JSONPath=`.status.gpuIndexesSummary`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SGPURuntimePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SGPURuntimePolicySpec `json:"spec"`

	// +optional
	Status SGPURuntimePolicyStatus `json:"status,omitempty"`
}

// SGPURuntimePolicyStatus carries controller-derived denormalizations of
// spec.targetRef. Its only purpose (today) is to power the default kubectl
// print columns with EndpointSlice-style comma-joined strings — CRD
// printer-column JSONPath cannot aggregate arrays, so the reconciler joins
// each targeting axis into a scalar string per column.
type SGPURuntimePolicyStatus struct {
	// RackGroupsSummary is a comma-joined rendering of spec.targetRef.rackGroups
	// (e.g. "training,ci"). Empty when spec.targetRef.rackGroups is unset,
	// which reads as "policy applies to every rack group of the inventory".
	// +optional
	RackGroupsSummary string `json:"rackGroupsSummary,omitempty"`

	// RackIndexesSummary is a comma-joined rendering of
	// spec.targetRef.rackIndexes (e.g. "0,1,2"). Empty when unset.
	// +optional
	RackIndexesSummary string `json:"rackIndexesSummary,omitempty"`

	// NodeIndexesSummary is a comma-joined rendering of
	// spec.targetRef.nodeIndexes (e.g. "5,10"). Empty when unset.
	// +optional
	NodeIndexesSummary string `json:"nodeIndexesSummary,omitempty"`

	// GPUIndexesSummary is a comma-joined rendering of
	// spec.targetRef.gpuIndexes (e.g. "1,3"). Empty when unset.
	// +optional
	GPUIndexesSummary string `json:"gpuIndexesSummary,omitempty"`
}

// SGPURuntimePolicyList is the list wrapper for SGPURuntimePolicy.
// +kubebuilder:object:root=true
type SGPURuntimePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPURuntimePolicy `json:"items"`
}

// SGPURuntimePolicySpec pairs a target selector with the RuntimeState to apply.
type SGPURuntimePolicySpec struct {
	TargetRef PolicyTargetRef `json:"targetRef"`

	// +optional
	Runtime *RuntimeState `json:"runtime,omitempty"`
}

// PolicyTargetRef selects the fan-out scope. Increasing specificity: whole
// inventory → subset of rack groups → subset of rack indices → subset of
// node indices → subset of GPU indices. Each optional slice narrows the
// previous level. Cross-field validation (rackIndex < count etc.) is done by
// the controller, not the schema — see MEP0001 §SGPURuntimePolicy.
type PolicyTargetRef struct {
	// +kubebuilder:validation:Enum=mokka.nvidia.com
	Group string `json:"group"`

	// +kubebuilder:validation:Enum=SGPUInventory
	Kind string `json:"kind"`

	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	RackGroups []string `json:"rackGroups,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	RackIndexes []int32 `json:"rackIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	NodeIndexes []int32 `json:"nodeIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	GPUIndexes []int32 `json:"gpuIndexes,omitempty"`
}
