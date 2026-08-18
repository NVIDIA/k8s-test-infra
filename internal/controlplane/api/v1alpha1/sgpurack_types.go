// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// SGPURack is the controller-owned materialization of one inventory rack.
// Its controller owner reference identifies the inventory pinned by
// spec.inventoryRef.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka,shortName=sgpur
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Inventory",type=string,JSONPath=`.spec.inventoryRef.name`
// +kubebuilder:printcolumn:name="Rack Group",type=string,JSONPath=`.spec.identity.rackGroup`
// +kubebuilder:printcolumn:name="Rack",type=integer,JSONPath=`.spec.identity.rackIndex`
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.spec.profileRef.name`
// +kubebuilder:printcolumn:name="Assigned",type=integer,JSONPath=`.status.assignedNodes`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SGPURack struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SGPURackSpec `json:"spec"`

	// +optional
	Status SGPURackStatus `json:"status,omitempty"`
}

// SGPURackList is the list wrapper for SGPURack.
// +kubebuilder:object:root=true
type SGPURackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPURack `json:"items"`
}

// SGPURackSpec is a rendered rack with durable logical Node bindings.
type SGPURackSpec struct {
	// InventoryRef pins ownership to an exact inventory instance so a
	// same-name replacement cannot inherit this rack.
	InventoryRef SGPURackInventoryReference `json:"inventoryRef"`

	// ProfileRef records the exact profile revision used to render the rack.
	ProfileRef SGPURackProfileReference `json:"profileRef"`

	// Identity is the stable coordinate used by agents and topology consumers.
	Identity SGPURackIdentity `json:"identity"`

	// Nodes retain stable device identities while Kubernetes Node assignments
	// change. Fabric and network capabilities remain in the referenced profile.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1024
	// +listType=map
	// +listMapKey=index
	Nodes []SGPURackNode `json:"nodes"`
}

// SGPURackInventoryReference pins a rack to an exact inventory instance.
// +kubebuilder:validation:XValidation:rule="size(self.uid) > 0",message="uid must not be empty"
type SGPURackInventoryReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	UID types.UID `json:"uid"`
}

// SGPURackProfileReference pins rendered data to an exact profile revision.
// +kubebuilder:validation:XValidation:rule="size(self.uid) > 0",message="uid must not be empty"
type SGPURackProfileReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	UID types.UID `json:"uid"`

	// +kubebuilder:validation:Minimum=1
	Generation int64 `json:"generation"`

	// Revision identifies the rendered profile content, independent of its name.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{64}$`
	Revision string `json:"revision"`
}

// SGPURackIdentity contains deterministic rack coordinates and fabric identity.
type SGPURackIdentity struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Format=dns1123Label
	RackGroup string `json:"rackGroup"`

	// +kubebuilder:validation:Minimum=0
	RackIndex int32 `json:"rackIndex"`

	// +kubebuilder:validation:Format=uuid
	FabricUUID string `json:"fabricUUID"`

	// CliqueID remains zero while each rack represents one fabric clique.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=0
	CliqueID int32 `json:"cliqueID"`
}

// SGPURackNode is one logical Node and its optional Kubernetes Node binding.
type SGPURackNode struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1023
	Index int32 `json:"index"`

	// NodeRef is absent while the logical Node is unbound. Its UID distinguishes
	// a replacement Kubernetes Node that reuses the same name.
	// +optional
	NodeRef *SGPUNodeReference `json:"nodeRef,omitempty"`

	// GPUs are rendered once for the logical Node and do not depend on its binding.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=index
	GPUs []SGPURackGPU `json:"gpus"`
}

// SGPUNodeReference identifies an exact Kubernetes Node instance.
// +kubebuilder:validation:XValidation:rule="size(self.uid) > 0",message="uid must not be empty"
type SGPUNodeReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	UID types.UID `json:"uid"`
}

// SGPURackGPU contains deterministic identity and structural placement.
type SGPURackGPU struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=63
	Index int32 `json:"index"`

	// +kubebuilder:validation:Pattern=`^GPU-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	UUID string `json:"uuid"`

	// +kubebuilder:validation:MinLength=1
	Serial string `json:"serial"`

	// +kubebuilder:validation:Minimum=0
	MinorNumber int32 `json:"minorNumber"`

	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`
	PCIAddress string `json:"pciAddress"`

	// +kubebuilder:validation:Pattern=`^pci[0-9a-f]{4}:[0-9a-f]{2}$`
	RootComplex string `json:"rootComplex"`

	// +kubebuilder:validation:Minimum=0
	NUMANode int32 `json:"numaNode"`

	// +kubebuilder:validation:Minimum=0
	HostProcessorIndex int32 `json:"hostProcessorIndex"`
}

// SGPURackStatus summarizes durable logical Node assignments.
type SGPURackStatus struct {
	// ObservedGeneration is the latest spec generation reflected by this status.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// AssignedNodes is the number of logical Nodes with an exact Kubernetes
	// Node binding.
	// +optional
	// +kubebuilder:validation:Minimum=0
	AssignedNodes int32 `json:"assignedNodes,omitempty"`

	// Conditions report whether the rendered assignment is ready for agents.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// RackConditionReady reports whether the rack assignment is ready for use.
const RackConditionReady = "Ready"
