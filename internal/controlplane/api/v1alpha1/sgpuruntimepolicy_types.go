// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SGPURuntimePolicy applies a sparse RuntimeState override to a subset of the
// referenced SGPUInventory: a whole inventory, specific rack groups, specific
// rack indices, specific node indices, or specific GPU indices. Omitted
// spec.runtime fields inherit from the profile's defaults.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka,shortName=srpol
// +kubebuilder:printcolumn:name="TargetKind",type=string,JSONPath=`.spec.targetRef.kind`
// +kubebuilder:printcolumn:name="TargetName",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SGPURuntimePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SGPURuntimePolicySpec `json:"spec"`
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
