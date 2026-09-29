// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SGPUInventory is a set of simulated GPU racks to distribute across CPU nodes.
//
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:metadata:annotations=helm.sh/resource-policy=keep
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka,shortName=sinv
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Groups",type=string,JSONPath=`.status.rackGroupsSummary`
// +kubebuilder:printcolumn:name="Nodes",type=integer,JSONPath=`.status.capacity.nodes`
// +kubebuilder:printcolumn:name="GPUs",type=integer,JSONPath=`.status.capacity.gpus`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SGPUInventory struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec describes the desired rack composition.
	Spec SGPUInventorySpec `json:"spec"`

	// +optional
	// Status reports realized capacity and allocation state.
	Status SGPUInventoryStatus `json:"status,omitempty"`
}

// SGPUInventoryList is the list wrapper for SGPUInventory.
// +kubebuilder:object:root=true
type SGPUInventoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPUInventory `json:"items"`
}

// SGPUInventorySpec is the desired rack composition.
type SGPUInventorySpec struct {
	// RackGroups declares homogeneous sets of racks. One group can expand to
	// many racks; the controller admits at most 64 groups across all Inventories.
	// +listType=map
	// +listMapKey=id
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// RackGroups declares the homogeneous rack groups in the inventory.
	RackGroups []RackGroup `json:"rackGroups"`
}

// RackGroup is a homogeneous group of racks sharing a profile.
type RackGroup struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// ID is the stable identifier for this rack group.
	ID string `json:"id"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100000
	// Count is the number of racks to materialize.
	Count int32 `json:"count"`

	// ProfileRef selects the rack profile used for this group.
	ProfileRef ProfileReference `json:"profileRef"`

	// +optional
	Placement *RackPlacement `json:"placement,omitempty"`
}

// ProfileReference targets an SGPURackProfile by name.
type ProfileReference struct {
	// +kubebuilder:validation:MinLength=1
	// Name is the SGPURackProfile resource name.
	Name string `json:"name"`
}

// RackPlacement constrains which nodes a rack group may materialize on.
type RackPlacement struct {
	// +optional
	// NodeSelector limits placement to matching Kubernetes nodes.
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// SGPUInventoryStatus is the Control Plane view of realized capacity and usage.
type SGPUInventoryStatus struct {
	// RackGroupsSummary is a comma-joined rendering of rack-group IDs.
	// +optional
	RackGroupsSummary string `json:"rackGroupsSummary,omitempty"`

	// +optional
	// Capacity reports the realized rack, node, and GPU counts.
	Capacity InventoryCapacity `json:"capacity,omitempty"`

	// +optional
	// Usage reports requested, allocated, available, and pending nodes.
	Usage InventoryUsage `json:"usage,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=id
	// RackGroups is the current per-group status projection.
	RackGroups []RackGroupStatus `json:"rackGroups,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// Conditions report inventory acceptance and programming state.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// InventoryCapacity is realized rack/node/GPU counts.
type InventoryCapacity struct {
	// Racks is the number of realized racks.
	Racks int32 `json:"racks"`
	// Nodes is the number of realized logical nodes.
	Nodes int32 `json:"nodes"`
	// GPUs is the number of realized GPUs.
	GPUs int32 `json:"gpus"`
}

// InventoryUsage is node-level request/allocation counts.
type InventoryUsage struct {
	// RequestedNodes is the number of nodes requested by consumers.
	RequestedNodes int32 `json:"requestedNodes"`
	// AllocatedNodes is the number of nodes assigned to consumers.
	AllocatedNodes int32 `json:"allocatedNodes"`
	// AvailableNodes is the number of currently available nodes.
	AvailableNodes int32 `json:"availableNodes"`
	// PendingNodes is the number of requests awaiting allocation.
	PendingNodes int32 `json:"pendingNodes"`
}

// RackGroupStatus is the per-group Capacity + Usage projection.
type RackGroupStatus struct {
	// ID identifies the rack group.
	ID string `json:"id"`
	// ProfileName is the profile used by the group.
	ProfileName string `json:"profileName"`
	// Capacity is the realized capacity for the group.
	Capacity InventoryCapacity `json:"capacity"`
	// Usage is the current allocation state for the group.
	Usage InventoryUsage `json:"usage"`
}

// SGPUInventory condition types.
const (
	InventoryConditionAccepted          = "Accepted"
	InventoryConditionResolvedRefs      = "ResolvedRefs"
	InventoryConditionProgrammed        = "Programmed"
	InventoryConditionRequestsSatisfied = "RequestsSatisfied"
)
