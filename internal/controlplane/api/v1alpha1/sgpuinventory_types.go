// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SGPUInventory declares a set of simulated GPU racks the Control Plane
// should distribute across CPU nodes. Rack groups are the associative unit:
// each group specifies a profile, a count, and an optional node selector.
//
// All print columns resolve to .status.* fields the (future) controller
// writes: Groups, Nodes, and GPUs stay blank until the reconciler runs.
// Groups uses a controller-maintained pre-joined string ("training,ci")
// modeled after how the kube-apiserver's hard-coded EndpointSlice printer
// renders its ENDPOINTS column — CRD printer-column JSONPath can't
// aggregate arrays, and rendering .spec.rackGroups directly is unreadable
// (raw struct JSON) or misleading (first element only for a [*].id
// projection).
//
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

	Spec SGPUInventorySpec `json:"spec"`

	// +optional
	Status SGPUInventoryStatus `json:"status,omitempty"`
}

// SGPUInventoryList is the list wrapper for SGPUInventory.
// +kubebuilder:object:root=true
type SGPUInventoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPUInventory `json:"items"`
}

// SGPUInventorySpec declares the desired rack composition. The 64-group cap
// mirrors Gateway API's per-listener limit and keeps status reconciliation
// bounded.
type SGPUInventorySpec struct {
	// +listType=map
	// +listMapKey=id
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	RackGroups []RackGroup `json:"rackGroups"`
}

// RackGroup is one homogeneous group of racks sharing a profile.
type RackGroup struct {
	// ID uniquely identifies this group inside the inventory (also used to
	// key server-side-apply patches).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id"`

	// +kubebuilder:validation:Minimum=1
	Count int32 `json:"count"`

	ProfileRef ProfileReference `json:"profileRef"`

	// +optional
	Placement *RackPlacement `json:"placement,omitempty"`
}

// ProfileReference points at an SGPUProfile by name (cluster-scoped, so no
// namespace needed).
type ProfileReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// RackPlacement constrains which CPU nodes a rack group may materialize on.
type RackPlacement struct {
	// +optional
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// SGPUInventoryStatus holds the Control Plane's view of realized capacity,
// current usage, and per-rack-group breakdowns.
type SGPUInventoryStatus struct {
	// RackGroupsSummary is a comma-joined list of the current rack-group
	// IDs (for example, "training,ci"). Denormalized so the default kubectl
	// print column can render EndpointSlice-style — CRD printer-column
	// JSONPath can't aggregate arrays, so a pre-joined string is the only
	// readable option.
	// +optional
	RackGroupsSummary string `json:"rackGroupsSummary,omitempty"`

	// +optional
	Capacity InventoryCapacity `json:"capacity,omitempty"`

	// +optional
	Usage InventoryUsage `json:"usage,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=id
	RackGroups []RackGroupStatus `json:"rackGroups,omitempty"`

	// Conditions reports the reconciliation state. Standard condition types
	// are declared as InventoryCondition* constants in this package.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// InventoryCapacity aggregates realized rack/node/GPU counts.
type InventoryCapacity struct {
	Racks int32 `json:"racks"`
	Nodes int32 `json:"nodes"`
	GPUs  int32 `json:"gpus"`
}

// InventoryUsage aggregates request/allocation lifecycle counts at node
// granularity — the Control Plane's coarse view of how much capacity is spoken for.
type InventoryUsage struct {
	RequestedNodes int32 `json:"requestedNodes"`
	AllocatedNodes int32 `json:"allocatedNodes"`
	AvailableNodes int32 `json:"availableNodes"`
	PendingNodes   int32 `json:"pendingNodes"`
}

// RackGroupStatus is the per-group projection of Capacity and Usage plus the
// resolved profile name (which may drift if profileRef changes without a spec
// bump).
type RackGroupStatus struct {
	ID          string            `json:"id"`
	ProfileName string            `json:"profileName"`
	Capacity    InventoryCapacity `json:"capacity"`
	Usage       InventoryUsage    `json:"usage"`
}

// Condition types the Control Plane reports on SGPUInventory. Matches
// MEP0001 §SGPUInventory example status.conditions[*].type.
const (
	InventoryConditionAccepted          = "Accepted"
	InventoryConditionResolvedRefs      = "ResolvedRefs"
	InventoryConditionProgrammed        = "Programmed"
	InventoryConditionRequestsSatisfied = "RequestsSatisfied"
)
