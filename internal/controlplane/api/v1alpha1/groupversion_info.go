// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the API group all Mokka Control Plane CRDs live under.
const GroupName = "mokka.nvidia.com"

var (
	// GroupVersion is the group + version this package registers into a Scheme.
	GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

	// SchemeGroupVersion aliases GroupVersion for code-generator compatibility.
	SchemeGroupVersion = GroupVersion

	// SchemeBuilder collects registration funcs for the types in this package.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme installs Mokka CRD types into the given Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// Resource returns a GroupResource for `resource` in this package's group.
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
