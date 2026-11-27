// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package v1alpha1

import (
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// SGPURackAllocation records container allocations for one rack. The KWOK data
// plane names the record after its owning rack and updates it with resourceVersion.
//
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:metadata:annotations=helm.sh/resource-policy=keep
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=mokka
type SGPURackAllocation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Nodes lists the rack's nodes with recorded allocations.
	// +optional
	// +listType=map
	// +listMapKey=name
	Nodes []NodeAllocation `json:"nodes,omitempty"`
}

// NewSGPURackAllocation ties a new record to the exact rack instance so
// Kubernetes garbage-collects the allocations when that rack is deleted.
func NewSGPURackAllocation(rack *SGPURack) *SGPURackAllocation {
	return &SGPURackAllocation{
		ObjectMeta: metav1.ObjectMeta{
			Name: rack.Name,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: GroupVersion.String(),
				Kind:       "SGPURack",
				Name:       rack.Name,
				UID:        rack.UID,
			}},
		},
	}
}

// OwnedByRack reports whether the record can be reused for this rack instance.
// A second owner can keep a record alive after the original rack is deleted.
func (a *SGPURackAllocation) OwnedByRack(rack *SGPURack) bool {
	if a == nil || rack == nil || rack.Name == "" || rack.UID == "" ||
		a.Name != rack.Name || len(a.OwnerReferences) != 1 {
		return false
	}
	owner := a.OwnerReferences[0]
	return owner.APIVersion == GroupVersion.String() && owner.Kind == "SGPURack" &&
		owner.Name == rack.Name && owner.UID == rack.UID
}

// Keep one acknowledgement per slot, including when a deleted Node no longer
// has managed fields. A later binding must never reuse an old acknowledgement.
func releaseAnnotation(index int32) string {
	return fmt.Sprintf("mokka.nvidia.com/released-slot-%d", index)
}

// SlotReleased reports whether cleanup finished for this exact publication.
func (a *SGPURackAllocation) SlotReleased(index int32, uid types.UID, revision int64) bool {
	return a.Annotations[releaseAnnotation(index)] == fmt.Sprintf("%s/%d", uid, revision)
}

// MarkSlotReleased records completion after the final Node write.
func (a *SGPURackAllocation) MarkSlotReleased(index int32, uid types.UID, revision int64) {
	if a.Annotations == nil {
		a.Annotations = make(map[string]string)
	}
	a.Annotations[releaseAnnotation(index)] = fmt.Sprintf("%s/%d", uid, revision)
}

// NodeAllocation groups the Pods allocated resources on one node.
type NodeAllocation struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +listType=map
	// +listMapKey=uid
	Pods []PodAllocation `json:"pods"`
}

// PodAllocation uses the Pod UID to distinguish replacements with the same name.
type PodAllocation struct {
	// Pod identifies the Pod as namespace/name.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?/[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Pod string `json:"pod"`

	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MinLength=1
	UID types.UID `json:"uid"`

	// +listType=map
	// +listMapKey=name
	Containers []ContainerAllocation `json:"containers"`
}

// ContainerAllocation records the GPU identities and optional NUMA placement.
type ContainerAllocation struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +optional
	// +listType=set
	// +kubebuilder:validation:items:Pattern=`^GPU-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	GPUs []string `json:"gpus,omitempty"`

	// NUMAZone is absent when no zone was assigned. Zero is a valid zone.
	// +optional
	// +kubebuilder:validation:Minimum=0
	NUMAZone *int32 `json:"numaZone,omitempty"`
}

// SGPURackAllocationList contains rack allocation records.
// +kubebuilder:object:root=true
type SGPURackAllocationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SGPURackAllocation `json:"items"`
}
