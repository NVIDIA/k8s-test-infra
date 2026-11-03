// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import corev1 "k8s.io/api/core/v1"

// Provisioner names the service that brings a rack's Nodes into existence.
// +kubebuilder:validation:Enum=CPU;KWOK
type Provisioner string

const (
	// ProvisionerCPU binds racks to existing CPU Nodes, where the node daemon
	// simulates the GPUs on the host.
	ProvisionerCPU Provisioner = "CPU"

	// ProvisionerKWOK creates a virtual Node for each logical rack Node. The
	// Nodes have no kubelet and their GPUs exist only in the Kubernetes API.
	ProvisionerKWOK Provisioner = "KWOK"
)

// Effective resolves an unset provisioner to CPU, matching the CRD default.
// Objects that went through admission always carry one; objects built in
// process may not.
func (p Provisioner) Effective() Provisioner {
	if p == "" {
		return ProvisionerCPU
	}

	return p
}

// KWOKSettings configures the virtual Nodes created for KWOK provisioning.
// These settings are absent from CPU racks, where real Nodes already supply
// everything they describe.
type KWOKSettings struct {
	// +optional
	// Kubelet is the kubelet configuration the Nodes report. There is no
	// kubelet on a KWOK Node, so Mokka reports what one would.
	Kubelet *KWOKKubelet `json:"kubelet,omitempty"`

	// +optional
	// NodeTemplate is the metadata applied to every Node created for the rack.
	NodeTemplate *KWOKNodeTemplate `json:"nodeTemplate,omitempty"`
}

// KWOKKubelet is the kubelet configuration a virtual Node reports. These
// settings stay out of SGPURackProfile because the same profile also backs CPU
// rack groups, where a real kubelet decides them.
type KWOKKubelet struct {
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=110
	// MaxPods is the pod capacity of each Node, defaulting to the kubelet's own
	// default.
	MaxPods int32 `json:"maxPods,omitempty"`
}

// KWOKNodeTemplate is the metadata stamped on every Node a KWOK rack creates.
type KWOKNodeTemplate struct {
	// +optional
	// Labels are added to every Node created for the rack. They may not set the
	// labels Mokka owns: mokka.nvidia.com/sgpu-node, which would offer the
	// virtual Nodes to CPU racks, and mokka.nvidia.com/provisioner, which Mokka
	// sets to route the Node to its own provisioner.
	// +kubebuilder:validation:XValidation:rule="!('mokka.nvidia.com/sgpu-node' in self) && !('mokka.nvidia.com/provisioner' in self)",message="labels must not set mokka.nvidia.com/sgpu-node or mokka.nvidia.com/provisioner, which Mokka owns"
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	// Taints are applied to every Node created for the rack, so that ordinary
	// workloads stay off the virtual Nodes unless they tolerate them.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=32
	Taints []corev1.Taint `json:"taints,omitempty"`
}
