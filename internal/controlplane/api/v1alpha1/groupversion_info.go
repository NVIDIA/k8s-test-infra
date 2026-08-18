// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the Mokka Control Plane API group.
const GroupName = "mokka.nvidia.com"

var (
	// GroupVersion is the registered GroupVersion.
	GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

	// SchemeGroupVersion aliases GroupVersion for code-generator compatibility.
	SchemeGroupVersion = GroupVersion

	// SchemeBuilder registers Mokka API types.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme installs Mokka API types into a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// Resource returns a group-qualified GroupResource.
func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&SGPUProfile{},
		&SGPUProfileList{},
		&SGPUInventory{},
		&SGPUInventoryList{},
		&SGPURuntimePolicy{},
		&SGPURuntimePolicyList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
