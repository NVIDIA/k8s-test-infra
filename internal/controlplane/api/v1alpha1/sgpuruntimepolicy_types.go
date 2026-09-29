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

	// Spec selects the inventory scope and runtime override.
	Spec SGPURuntimePolicySpec `json:"spec"`

	// +optional
	// Status contains denormalized target axes for print columns.
	Status SGPURuntimePolicyStatus `json:"status,omitempty"`
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
	// TargetRef identifies the inventory and optional rack, node, and GPU axes.
	TargetRef PolicyTargetRef `json:"targetRef"`

	// +optional
	// Runtime is the sparse runtime override to apply.
	Runtime *RuntimeState `json:"runtime,omitempty"`
}

// PolicyTargetRef selects the fan-out scope. Each optional slice narrows
// the level above.
type PolicyTargetRef struct {
	// +kubebuilder:validation:Enum=mokka.nvidia.com
	// Group is the API group of the selected inventory.
	Group string `json:"group"`

	// +kubebuilder:validation:Enum=SGPUInventory
	// Kind is the resource kind being selected.
	Kind string `json:"kind"`

	// +kubebuilder:validation:MinLength=1
	// Name is the selected SGPUInventory resource name.
	Name string `json:"name"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// RackGroups limits the policy to these rack-group IDs.
	RackGroups []string `json:"rackGroups,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// RackIndexes limits the policy to these rack indexes.
	RackIndexes []int32 `json:"rackIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// NodeIndexes limits the policy to these node indexes.
	NodeIndexes []int32 `json:"nodeIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// GPUIndexes limits the policy to these GPU indexes.
	GPUIndexes []int32 `json:"gpuIndexes,omitempty"`
}

// SGPURuntimePolicyStatus holds comma-joined denormalizations of
// spec.targetRef axes for print columns.
type SGPURuntimePolicyStatus struct {
	// +optional
	// RackGroupsSummary lists the selected rack groups.
	RackGroupsSummary string `json:"rackGroupsSummary,omitempty"`

	// +optional
	// RackIndexesSummary lists the selected rack indexes.
	RackIndexesSummary string `json:"rackIndexesSummary,omitempty"`

	// +optional
	// NodeIndexesSummary lists the selected node indexes.
	NodeIndexesSummary string `json:"nodeIndexesSummary,omitempty"`

	// +optional
	// GPUIndexesSummary lists the selected GPU indexes.
	GPUIndexesSummary string `json:"gpuIndexesSummary,omitempty"`
}
