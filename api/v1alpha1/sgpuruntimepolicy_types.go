// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SGPURuntimePolicy applies a sparse RuntimeState override to a subset of
// an SGPUInventory.
//
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:metadata:annotations=helm.sh/resource-policy=keep
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

	// Spec defines the target selection and runtime settings to apply.
	Spec SGPURuntimePolicySpec `json:"spec"`

	// +optional
	// Status summarizes the target scope selected for this policy.
	Status SGPURuntimePolicyStatus `json:"status,omitempty"`
}

// SGPURuntimePolicyList is the list wrapper for SGPURuntimePolicy.
// +kubebuilder:object:root=true
type SGPURuntimePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the runtime policies in this list.
	Items []SGPURuntimePolicy `json:"items"`
}

// SGPURuntimePolicySpec pairs a target selector with the RuntimeState to apply.
type SGPURuntimePolicySpec struct {
	// TargetRef selects the inventory and optional rack, Node, and GPU subset.
	TargetRef PolicyTargetRef `json:"targetRef"`

	// +optional
	// Runtime contains sparse settings to apply to the selected GPUs.
	Runtime *RuntimeState `json:"runtime,omitempty"`
}

// PolicyTargetRef selects the fan-out scope. Each optional slice narrows
// the level above.
type PolicyTargetRef struct {
	// +kubebuilder:validation:Enum=mokka.nvidia.com
	// Group is the API group containing the target resource.
	Group string `json:"group"`

	// +kubebuilder:validation:Enum=SGPUInventory
	// Kind is the target resource kind.
	Kind string `json:"kind"`

	// +kubebuilder:validation:MinLength=1
	// Name is the name of the target SGPUInventory.
	Name string `json:"name"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// RackGroups optionally limits the policy to these rack-group IDs.
	RackGroups []string `json:"rackGroups,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// RackIndexes optionally limits the policy to these rack indexes.
	RackIndexes []int32 `json:"rackIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// NodeIndexes optionally limits the policy to these logical Node indexes.
	NodeIndexes []int32 `json:"nodeIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// GPUIndexes optionally limits the policy to these GPU indexes.
	GPUIndexes []int32 `json:"gpuIndexes,omitempty"`
}

// SGPURuntimePolicyStatus holds comma-joined denormalizations of
// spec.targetRef axes for print columns.
type SGPURuntimePolicyStatus struct {
	// +optional
	// RackGroupsSummary is the comma-separated selected rack-group IDs.
	RackGroupsSummary string `json:"rackGroupsSummary,omitempty"`

	// +optional
	// RackIndexesSummary is the comma-separated selected rack indexes.
	RackIndexesSummary string `json:"rackIndexesSummary,omitempty"`

	// +optional
	// NodeIndexesSummary is the comma-separated selected logical Node indexes.
	NodeIndexesSummary string `json:"nodeIndexesSummary,omitempty"`

	// +optional
	// GPUIndexesSummary is the comma-separated selected GPU indexes.
	GPUIndexesSummary string `json:"gpuIndexesSummary,omitempty"`
}
