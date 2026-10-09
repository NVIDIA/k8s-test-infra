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
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SGPURuntimePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SGPURuntimePolicySpec `json:"spec"`

	// +optional
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
	TargetRef PolicyTargetRef `json:"targetRef"`

	// +optional
	Runtime *RuntimeState `json:"runtime,omitempty"`
}

// PolicyTargetRef selects the simulated GPUs a policy applies to. Each listed
// axis narrows the one above it, and an omitted axis selects every index. The
// deepest listed axis is the policy's scope: inventory, rack group, rack, Node,
// or GPU. A narrower scope overrides a broader one field by field. Every
// listed rack group must exist in the inventory, and every listed index must
// exist in at least one selected rack group.
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
	// +kubebuilder:validation:items:Minimum=0
	RackIndexes []int32 `json:"rackIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Minimum=0
	NodeIndexes []int32 `json:"nodeIndexes,omitempty"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Minimum=0
	GPUIndexes []int32 `json:"gpuIndexes,omitempty"`
}

// SGPURuntimePolicyStatus reports whether the policy is accepted and holds
// comma-joined denormalizations of spec.targetRef axes for print columns.
type SGPURuntimePolicyStatus struct {
	// +optional
	RackGroupsSummary string `json:"rackGroupsSummary,omitempty"`

	// +optional
	RackIndexesSummary string `json:"rackIndexesSummary,omitempty"`

	// +optional
	NodeIndexesSummary string `json:"nodeIndexesSummary,omitempty"`

	// +optional
	GPUIndexesSummary string `json:"gpuIndexesSummary,omitempty"`

	// Conditions report whether the policy is accepted.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// RuntimePolicyConditionAccepted reports whether the policy is valid and
// applies to its target.
const RuntimePolicyConditionAccepted = "Accepted"
