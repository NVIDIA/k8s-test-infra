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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	SGPURackConditionReady          = "Ready"
	SGPURackConditionNodesProjected = "NodesProjected"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=sgpur
// +kubebuilder:subresource:status

// SGPURack is the controller-owned materialization of one rack.
type SGPURack struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SGPURackSpec   `json:"spec"`
	Status SGPURackStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SGPURackList contains a list of SGPURack resources.
type SGPURackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPURack `json:"items"`
}

// SGPURackSpec is a rendered rack with durable Node bindings.
type SGPURackSpec struct {
	InventoryRef SGPURackInventoryReference `json:"inventoryRef"`
	ProfileRef   SGPURackProfileReference   `json:"profileRef"`
	Identity     SGPURackIdentity           `json:"identity"`
	GPUFabric    *SGPUGPUFabric             `json:"gpuFabric,omitempty"`
	Network      *SGPUNetwork               `json:"network,omitempty"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1024
	// +listType=map
	// +listMapKey=index
	Slots []SGPURackSlot `json:"slots"`
}

// +kubebuilder:validation:XValidation:rule="self.uid != ''",message="uid must not be empty"

// SGPURackInventoryReference pins a rack to an exact inventory instance.
type SGPURackInventoryReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

// +kubebuilder:validation:XValidation:rule="self.uid != ''",message="uid must not be empty"

// SGPURackProfileReference pins rendered data to an exact profile revision.
type SGPURackProfileReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
	// +kubebuilder:validation:Minimum=1
	Generation int64 `json:"generation"`
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
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=0
	CliqueID int32 `json:"cliqueID"`
}

// SGPURackSlot is one durable allocation coordinate in a rack.
type SGPURackSlot struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1023
	Index   int32              `json:"index"`
	NodeRef *SGPUNodeReference `json:"nodeRef,omitempty"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=index
	GPUs []SGPURackGPU `json:"gpus"`
}

// +kubebuilder:validation:XValidation:rule="self.uid != ''",message="uid must not be empty"

// SGPUNodeReference identifies an exact Kubernetes Node instance.
type SGPUNodeReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
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

// SGPURackStatus summarizes assignment and successful Node projections.
type SGPURackStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +kubebuilder:validation:Minimum=0
	AssignedSlots int32 `json:"assignedSlots,omitempty"`
	// +kubebuilder:validation:Minimum=0
	ProjectedSlots int32 `json:"projectedSlots,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
