// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package v1alpha1 describes the compiled hardware state shared by rack and Node readers.
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/types"

	api "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

// Version identifies the wire format of a rack-state response.
const Version = "v1alpha1"

// Object identifies one instance, even if its name is reused.
type Object struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

// Hardware contains the facts shared by every assigned Node in the rack.
type Hardware struct {
	Rack                   Object            `json:"rack"`
	RackDeliveryGeneration int64             `json:"rackDeliveryGeneration"`
	Software               *api.SGPUSoftware `json:"software,omitempty"`
	GPUModel               GPUModel          `json:"gpuModel"`
	Fabric                 *Fabric           `json:"fabric,omitempty"`
}

// GPUModel describes the hardware common to all GPUs of this profile.
type GPUModel struct {
	Vendor            string           `json:"vendor,omitempty"`
	Product           string           `json:"product,omitempty"`
	ProductName       string           `json:"productName,omitempty"`
	Architecture      string           `json:"architecture,omitempty"`
	ComputeCapability string           `json:"computeCapability,omitempty"`
	Memory            string           `json:"memory"`
	PCI               api.GPUPCI       `json:"pci"`
	Cores             *api.GPUCores    `json:"cores,omitempty"`
	Board             *api.GPUBoard    `json:"board,omitempty"`
	Firmware          *api.GPUFirmware `json:"firmware,omitempty"`
}

// Fabric combines the profile topology with the identity of this rack.
type Fabric struct {
	FabricUUID string `json:"fabricUUID"`
	CliqueID   int32  `json:"cliqueID"`
	api.GPUFabric
}

// GPU combines a rendered GPU identity with its effective settings.
type GPU struct {
	api.SGPURackGPU
	Runtime api.RuntimeState `json:"runtime"`
}

// Slot carries the persisted assignment revision and rendered GPU identities.
type Slot struct {
	Index              int32  `json:"index"`
	Node               Object `json:"node"`
	AssignmentRevision int64  `json:"assignmentRevision"`
	Releasing          bool   `json:"releasing,omitempty"`
	GPUs               []GPU  `json:"gpus,omitempty"`
}

// Rack is the compiled entry for one rack in a batch response.
type Rack struct {
	Hardware
	Nodes []Slot `json:"nodes"`
}

// NodePayload uses the same hardware and slot types as a rack in a batch.
type NodePayload struct {
	APIVersion string `json:"apiVersion"`
	Hardware
	Slot
}

// ForNode selects an exact Node binding from the compiled rack.
func (r Rack) ForNode(node Object) (NodePayload, bool) {
	for _, slot := range r.Nodes {
		if slot.Node == node {
			return NodePayload{APIVersion: Version, Hardware: r.Hardware, Slot: slot}, true
		}
	}
	return NodePayload{}, false
}
