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

	// Spec defines the simulated rack groups managed by this inventory.
	Spec SGPUInventorySpec `json:"spec"`

	// +optional
	// Status reports the capacity and usage currently realized by the controller.
	Status SGPUInventoryStatus `json:"status,omitempty"`
}

// SGPUInventoryList is the list wrapper for SGPUInventory.
// +kubebuilder:object:root=true
type SGPUInventoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the inventories in this list.
	Items []SGPUInventory `json:"items"`
}

// SGPUInventorySpec is the desired rack composition.
type SGPUInventorySpec struct {
	// RackGroups declares homogeneous sets of racks. One group can expand to
	// many racks; the controller admits at most 64 groups across all Inventories.
	// +listType=map
	// +listMapKey=id
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	RackGroups []RackGroup `json:"rackGroups"`
}

// RackGroup is a homogeneous group of racks sharing a profile.
type RackGroup struct {
	// ID uniquely identifies this group within its inventory.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100000
	// Count is the number of racks to materialize from this group.
	Count int32 `json:"count"`

	// ProfileRef names the rack profile used to materialize each rack.
	ProfileRef ProfileReference `json:"profileRef"`

	// +optional
	// Placement optionally restricts the Nodes eligible for this rack group.
	Placement *RackPlacement `json:"placement,omitempty"`
}

// NodeSelector returns the group's placement Node selector, or nil when none
// is set.
func (g *RackGroup) NodeSelector() *metav1.LabelSelector {
	if g.Placement == nil {
		return nil
	}
	return g.Placement.NodeSelector
}

// ProfileReference targets an SGPURackProfile by name.
type ProfileReference struct {
	// +kubebuilder:validation:MinLength=1
	// Name is the name of the SGPURackProfile to use.
	Name string `json:"name"`
}

// RackPlacement constrains which nodes a rack group may materialize on.
type RackPlacement struct {
	// +optional
	// NodeSelector restricts placement to Nodes matching these labels.
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// SGPUInventoryStatus is the Control Plane view of realized capacity and usage.
type SGPUInventoryStatus struct {
	// RackGroupsSummary is a comma-joined rendering of rack-group IDs.
	// +optional
	RackGroupsSummary string `json:"rackGroupsSummary,omitempty"`

	// +optional
	// Capacity is the total rack, Node, and GPU capacity realized by this inventory.
	Capacity InventoryCapacity `json:"capacity,omitempty"`

	// +optional
	// Usage summarizes requested and allocated Nodes across the inventory.
	Usage InventoryUsage `json:"usage,omitempty"`

	// +optional
	// RackGroups contains per-group capacity and usage summaries.
	// +listType=map
	// +listMapKey=id
	RackGroups []RackGroupStatus `json:"rackGroups,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// Conditions report the latest inventory acceptance and programming results.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// InventoryCapacity is realized rack/node/GPU counts.
type InventoryCapacity struct {
	// Racks is the number of materialized racks.
	Racks int32 `json:"racks"`
	// Nodes is the number of logical Nodes across those racks.
	Nodes int32 `json:"nodes"`
	// GPUs is the number of simulated GPUs across those Nodes.
	GPUs int32 `json:"gpus"`
}

// InventoryUsage summarizes eligible placement Nodes, bound rack slots, and pending allocations.
type InventoryUsage struct {
	// RequestedNodes counts eligible Nodes matching placement selectors. Inventory totals
	// deduplicate matches across groups; per-group values count each group's matches.
	RequestedNodes int32 `json:"requestedNodes"`
	// AllocatedNodes counts rack Node slots with a live matching Kubernetes Node binding.
	AllocatedNodes int32 `json:"allocatedNodes"`
	// AvailableNodes is remaining capacity after bound rack slots, never below zero.
	AvailableNodes int32 `json:"availableNodes"`
	// PendingNodes counts eligible placement requests awaiting capacity in one matching group.
	PendingNodes int32 `json:"pendingNodes"`
}

// RackGroupStatus is the per-group Capacity + Usage projection.
type RackGroupStatus struct {
	// ID identifies the rack group summarized by this status.
	ID string `json:"id"`
	// ProfileName is the name of the profile used to materialize the group.
	ProfileName string `json:"profileName"`
	// Capacity is the realized capacity for this group.
	Capacity InventoryCapacity `json:"capacity"`
	// Usage summarizes Node allocation for this group.
	Usage InventoryUsage `json:"usage"`
}

// SGPUInventory condition types.
const (
	InventoryConditionAccepted          = "Accepted"
	InventoryConditionResolvedRefs      = "ResolvedRefs"
	InventoryConditionProgrammed        = "Programmed"
	InventoryConditionRequestsSatisfied = "RequestsSatisfied"
)
