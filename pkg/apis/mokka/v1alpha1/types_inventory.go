// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SGPUInventoryConditionAccepted          = "Accepted"
	SGPUInventoryConditionResolvedRefs      = "ResolvedRefs"
	SGPUInventoryConditionMaterialized      = "Materialized"
	SGPUInventoryConditionRequestsSatisfied = "RequestsSatisfied"
	SGPUInventoryConditionNodesProjected    = "NodesProjected"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=sgpui
// +kubebuilder:subresource:status

// SGPUInventory declares rack groups and their placement selectors.
type SGPUInventory struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SGPUInventorySpec   `json:"spec"`
	Status SGPUInventoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SGPUInventoryList contains a list of SGPUInventory resources.
type SGPUInventoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPUInventory `json:"items"`
}

// SGPUInventorySpec is the desired static inventory.
type SGPUInventorySpec struct {
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=id
	RackGroups []SGPURackGroup `json:"rackGroups"`
}

// SGPURackGroup declares a homogeneous set of racks.
type SGPURackGroup struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Format=dns1123Label
	ID string `json:"id"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100000
	Count int32 `json:"count"`
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="profileRef.name must not be empty"
	ProfileRef corev1.LocalObjectReference `json:"profileRef"`
	Placement  *SGPUPlacement              `json:"placement,omitempty"`
}

// SGPUPlacement restricts a rack group to eligible Nodes matching a selector.
type SGPUPlacement struct {
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// SGPUInventoryStatus summarizes resolved capacity and live usage.
type SGPUInventoryStatus struct {
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	Capacity           SGPUCapacity `json:"capacity,omitempty"`
	Usage              SGPUUsage    `json:"usage,omitempty"`
	// +listType=map
	// +listMapKey=id
	RackGroups []SGPURackGroupStatus `json:"rackGroups,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// SGPUCapacity contains resolved rack, Node-slot, and GPU totals.
type SGPUCapacity struct {
	// +kubebuilder:validation:Minimum=0
	Racks int64 `json:"racks"`
	// +kubebuilder:validation:Minimum=0
	NodeSlots int64 `json:"nodeSlots"`
	// +kubebuilder:validation:Minimum=0
	GPUs int64 `json:"gpus"`
}

// SGPUUsage contains allocation and projection counts.
type SGPUUsage struct {
	// +kubebuilder:validation:Minimum=0
	RequestedNodes int64 `json:"requestedNodes"`
	// +kubebuilder:validation:Minimum=0
	AllocatedNodes int64 `json:"allocatedNodes"`
	// +kubebuilder:validation:Minimum=0
	AvailableNodes int64 `json:"availableNodes"`
	// +kubebuilder:validation:Minimum=0
	PendingNodes int64 `json:"pendingNodes"`
	// +kubebuilder:validation:Minimum=0
	ConflictingNodes int64 `json:"conflictingNodes"`
	// +kubebuilder:validation:Minimum=0
	ProjectedNodes int64 `json:"projectedNodes"`
}

// SGPURackGroupStatus contains aggregate values for one declared group.
type SGPURackGroupStatus struct {
	ID       string       `json:"id"`
	Capacity SGPUCapacity `json:"capacity"`
	Usage    SGPUUsage    `json:"usage"`
}
